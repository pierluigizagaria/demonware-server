package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/platform/guest"
)

func ruleForTest(t *testing.T, name string) ctr.ChallengeRule {
	t.Helper()
	for _, r := range ctr.ChallengeScheduleSnapshot().Rules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("missing rule %s", name)
	return ctr.ChallengeRule{}
}

func ruleTime(r ctr.ChallengeRule) time.Time {
	return time.Unix(ctr.ChallengeScheduleSnapshot().CycleStart+30*ctr.ChallengeScheduleSnapshot().CycleSeconds+r.Periods[0].Start+60, 0)
}

func ruleEvent(r ctr.ChallengeRule, now time.Time) ctr.AchievementEvent {
	p := r.Periods[0]
	e := ctr.AchievementEvent{Name: r.Name, Timestamp: uint64(now.Unix()), Values: map[string]uint64{"Progress": 1, "X": uint64(p.X), "Y": uint64(p.Y)}}
	for k, vs := range p.Filters {
		e.Values[k] = ctr.ChallengeNameHash(vs[0])
	}
	return e
}

func TestSQLiteUnlockAllOwnsCatalogWithoutWrites(t *testing.T) {
	testStartBalance := int64(0)
	s, db := newTestEconomy(t, true, testStartBalance)
	a, err := s.Inventory(42)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Items, ctr.CatalogItemsSnapshot()) || len(ctr.CatalogItemsSnapshot()) == 0 {
		t.Fatal("inventory differs from catalog")
	}
	for _, sku := range ctr.EconomyCatalogSnapshot() {
		if !ctr.OwnsSKU(a, sku) {
			t.Fatal("catalog SKU not owned")
		}
	}
	if got := ctr.InventoryPage(a, 1, 4096); len(got) != len(ctr.CatalogItemsSnapshot()) {
		t.Fatalf("first page = %d items", len(got))
	}
	before := sqliteChanges(t, db)
	if debit, err := s.Purchase(42, []uint32{200085, 200597}); err != nil || debit != 0 {
		t.Fatalf("purchase = %d, %v", debit, err)
	}
	if _, err := s.Purchase(42, []uint32{200085, 1}); err == nil {
		t.Fatal("unknown SKU accepted")
	}
	if got := sqliteChanges(t, db); got != before {
		t.Fatalf("unlock-all wrote rows: %d -> %d", before, got)
	}
	for _, table := range []string{"accounts", "balances", "inventory", "ledger"} {
		if got := sqliteCount(t, db, table); got != 0 {
			t.Fatalf("%s has %d rows", table, got)
		}
	}
}

func TestSQLiteNormalInventoryIsPersisted(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 10000
	s, _ := newTestEconomy(t, false, testStartBalance)
	a, err := s.Inventory(42)
	if err != nil || len(a.Items) != 0 {
		t.Fatalf("fresh inventory = %v, %v", a, err)
	}
	if _, err := s.Purchase(42, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	a, err = s.Inventory(42)
	if err != nil || len(a.Items) != len(ctr.EconomyCatalogSnapshot()[200085].Items) || a.Balances[ctr.WumpaCurrency] != 10000-ctr.EconomyCatalogSnapshot()[200085].Price {
		t.Fatal("inventory does not reflect persisted purchases")
	}
}

func capturedRace(at time.Time) ctr.AchievementEvent {
	return ctr.AchievementEvent{Name: "end_race", Timestamp: uint64(at.Unix()), Values: map[string]uint64{"Track": 3273086243, "RaceMode": 1, "Rank": 2, "CompletedLap": 3, "Time": 114, "Online": 0, "RewardGoldenWumpa": 0}}
}

func openTestLeaderboard(t *testing.T, path string) *SQLite {
	t.Helper()
	s, err := OpenSQLite(path, false, testTrustID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The SQLite store must answer exactly like the upstream JSON
func TestLeaderboardStoresAgree(t *testing.T) {
	stat := func(board uint32, pid uint64, score int64, op byte, name string) ctr.StatWrite {
		return ctr.StatWrite{Stat: ctr.Stat{Board: board, PID: pid, Score: score, Driver: 3, Name: name, Updated: 77}, Operation: op}
	}
	batches := [][]ctr.StatWrite{
		{stat(1001, 12, 400, 4, "A"), stat(1001, 13, 500, 4, "B"), stat(1001, math.MaxUint64, 450, 4, "C")},
		{stat(1001, 12, 300, 4, "slower"), stat(1001, 13, 600, 4, "faster")},
		{stat(89, 12, 10, 1, "A"), stat(89, 12, 15, 1, "A"), stat(89, 14, 25, 0, "D")},
		{stat(89, 12, 25, 1, "tie")},
	}
	json := &ctr.StatsStore{Dir: t.TempDir()}
	sqlite := openTestLeaderboard(t, filepath.Join(t.TempDir(), DBFile))
	for _, b := range batches {
		if err := json.Write(b); err != nil {
			t.Fatal(err)
		}
		if err := sqlite.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	for _, board := range []uint32{1001, 89, 1002} {
		want, err1 := json.Read(board)
		got, err2 := sqlite.Read(board)
		if err1 != nil || err2 != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("board %d: sqlite %+v, json %+v (%v, %v)", board, got, want, err1, err2)
		}
	}
	if rows, _ := sqlite.Read(1001); rows[0].PID != 13 || rows[0].Score != 600 || rows[0].Name != "faster" || rows[1].PID != math.MaxUint64 {
		t.Fatalf("wrong ranking %+v", rows)
	}
}

func TestSQLiteLeaderboardBatchIsAtomicAndDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), DBFile)
	s := openTestLeaderboard(t, path)
	if err := s.Write([]ctr.StatWrite{{Stat: ctr.Stat{Board: 89, PID: 1, Score: math.MaxInt64 - 1}, Operation: 1}}); err != nil {
		t.Fatal(err)
	}
	overflow := []ctr.StatWrite{
		{Stat: ctr.Stat{Board: 1001, PID: 2, Score: 9}, Operation: 4},
		{Stat: ctr.Stat{Board: 89, PID: 1, Score: 2}, Operation: 1},
	}
	if err := s.Write(overflow); err == nil {
		t.Fatal("overflow accepted")
	}
	if rows, err := s.Read(1001); err != nil || len(rows) != 0 {
		t.Fatalf("partial batch committed: %+v, %v", rows, err)
	}
	s.Close()
	reopened := openTestLeaderboard(t, path)
	rows, err := reopened.Read(89)
	if err != nil || len(rows) != 1 || rows[0].Score != math.MaxInt64-1 || rows[0].Rank != 1 {
		t.Fatalf("reopened rows %+v, %v", rows, err)
	}
}

func TestSQLiteReplayStateRetention(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 0
	s, db := newTestEconomy(t, false, testStartBalance)
	now := time.Now().UTC()
	first := capturedRace(now)
	first.Values["Online"] = 1
	if amount, err := s.AwardRaces(42, []ctr.AchievementEvent{first}, now); err != nil || amount == 0 {
		t.Fatalf("first race: %d, %v", amount, err)
	}
	// Age the stored replay state beyond the retention window.
	old := now.Add(-ReplayRetention - time.Hour).Unix()
	sqliteExec(t, db, `UPDATE race_receipts SET recorded_at = ?`, old)
	sqliteExec(t, db, `UPDATE online_seconds SET updated_at = ?`, old)
	if a, err := s.Snapshot(42); err != nil || len(a.RaceReceipts) != 0 || len(a.OnlineSeconds) != 0 {
		t.Fatalf("expired replay state loaded: %+v, %v", a, err)
	}
	// A receipt still stored but outside the window is not inserted twice.
	if _, err := s.AwardRaces(42, []ctr.AchievementEvent{first}, now); err != nil {
		t.Fatal(err)
	}
	second := capturedRace(now.Add(-time.Minute))
	if _, err := s.AwardRaces(42, []ctr.AchievementEvent{second}, now); err != nil {
		t.Fatal(err)
	}
	var expired int
	if err := db.DB.QueryRow(`SELECT (SELECT count(*) FROM race_receipts WHERE recorded_at < ?1) +
		(SELECT count(*) FROM online_seconds WHERE updated_at < ?1)`, now.Add(-ReplayRetention).Unix()).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if expired != 0 || sqliteCount(t, db, "race_receipts") != 1 {
		t.Fatalf("expired replay state kept: %d expired, %d receipts", expired, sqliteCount(t, db, "race_receipts"))
	}
	if a, err := s.Snapshot(42); err != nil || len(a.RaceReceipts) != 1 {
		t.Fatalf("current receipt lost: %+v, %v", a, err)
	}
}

func TestSQLiteSaveWritesOnlyChangedRows(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 1000
	s, db := newTestEconomy(t, false, testStartBalance)
	now := time.Now().UTC()
	if _, err := s.AwardRaces(42, []ctr.AchievementEvent{capturedRace(now)}, now); err != nil {
		t.Fatal(err)
	}
	before := sqliteChanges(t, db)
	if _, err := s.AwardRaces(42, []ctr.AchievementEvent{capturedRace(now.Add(-time.Minute))}, now); err != nil {
		t.Fatal(err)
	}
	// One ledger row, one balance update, one receipt; nothing rewritten.
	if changed := sqliteChanges(t, db) - before; changed != 3 {
		t.Fatalf("reward changed %d rows", changed)
	}
}

func TestSQLiteRankedReadsMatchReference(t *testing.T) {
	json := &ctr.StatsStore{Dir: t.TempDir()}
	db := openTestLeaderboard(t, filepath.Join(t.TempDir(), DBFile))
	var writes []ctr.StatWrite
	for i, pid := range []uint64{5, 1 << 63, 7, math.MaxUint64, 2, 9, 1<<63 + 4, 11} {
		writes = append(writes, ctr.StatWrite{Stat: ctr.Stat{Board: 89, PID: pid, Score: int64(i % 3), Name: "p"}, Operation: 0})
	}
	for _, storage := range []ctr.LeaderboardStore{json, db} {
		if err := storage.Write(writes); err != nil {
			t.Fatal(err)
		}
	}
	for _, page := range [][2]uint64{{0, 3}, {1, 8}, {3, 2}, {8, 5}, {9, 5}, {math.MaxUint64, 1}, {2, 0}} {
		want, wantTotal, err1 := ctr.ReadLeaderboard(json, 4, 89, page[0], uint32(page[1]), nil)
		got, gotTotal, err2 := ctr.ReadLeaderboard(db, 4, 89, page[0], uint32(page[1]), nil)
		// The handler maps start 0 to 1 before reading.
		if page[0] == 0 {
			want, wantTotal, err1 = ctr.ReadLeaderboard(json, 4, 89, 1, uint32(page[1]), nil)
		}
		if err1 != nil || err2 != nil || gotTotal != wantTotal || !reflect.DeepEqual(got, want) {
			t.Fatalf("page %v: sqlite %+v/%d, reference %+v/%d (%v, %v)", page, got, gotTotal, want, wantTotal, err1, err2)
		}
	}
	ids := map[uint64]bool{math.MaxUint64: true, 2: true, 1 << 63: true, 404: true}
	want, wantTotal, err1 := ctr.ReadLeaderboard(json, 13, 89, 0, 0, ids)
	got, gotTotal, err2 := ctr.ReadLeaderboard(db, 13, 89, 0, 0, ids)
	if err1 != nil || err2 != nil || gotTotal != wantTotal || !reflect.DeepEqual(got, want) {
		t.Fatalf("entities: sqlite %+v/%d, reference %+v/%d", got, gotTotal, want, wantTotal)
	}
	var plan strings.Builder
	rs, err := db.DB.Query(`EXPLAIN QUERY PLAN SELECT pid FROM leaderboard WHERE board = ? ORDER BY `+LeaderboardOrder+` LIMIT 3 OFFSET 2`, 89)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var id, parent, unused int
		var detail string
		if err := rs.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	rs.Close()
	if !strings.Contains(plan.String(), "leaderboard_rank") || strings.Contains(plan.String(), "TEMP B-TREE") {
		t.Fatalf("rank page is not an index scan:\n%s", plan.String())
	}
}

const testTrustID = "test-trust"

func openTestCTR(t *testing.T, path string, unlockAll bool, initialBalance int64) *SQLite {
	t.Helper()
	db, err := OpenSQLite(path, unlockAll, testTrustID)
	if err != nil {
		t.Fatal(err)
	}
	db.InitialBalance = func() int64 { return initialBalance }
	t.Cleanup(func() { db.Close() })
	return db
}

func sqliteCount(t *testing.T, db *SQLite, table string) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sqliteChanges(t *testing.T, db *SQLite) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRow("SELECT total_changes()").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sqliteExec(t *testing.T, db *SQLite, query string, args ...any) {
	t.Helper()
	if _, err := db.DB.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func accountJSON(t *testing.T, s *ctr.EconomyStore, pid uint64) []byte {
	t.Helper()
	a, err := s.Snapshot(pid)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type economyResult struct {
	credit int64
	update []byte
	failed bool
}

// Exercise unchanged domain logic against JSON and SQLite, reopening both after
// every step. JSON encoding normalizes nil/empty maps omitted by the reference.
func TestEconomyStoresAgree(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 20000
	reference := &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	s, db := newTestEconomy(t, false, testStartBalance)
	const pid = uint64(math.MaxUint64)
	type step struct {
		name string
		run  func(*ctr.EconomyStore) economyResult
	}
	purchase := func(ids ...uint32) func(*ctr.EconomyStore) economyResult {
		return func(s *ctr.EconomyStore) economyResult {
			c, err := s.Purchase(pid, ids)
			return economyResult{credit: c, failed: err != nil}
		}
	}
	race := func(now time.Time, events ...ctr.AchievementEvent) func(*ctr.EconomyStore) economyResult {
		return func(s *ctr.EconomyStore) economyResult {
			c, err := s.AwardRaces(pid, events, now)
			return economyResult{credit: c, failed: err != nil}
		}
	}
	challenge := func(now time.Time, events ...ctr.AchievementEvent) func(*ctr.EconomyStore) economyResult {
		return func(s *ctr.EconomyStore) economyResult {
			c, u, err := s.AwardChallenges(pid, events, now)
			return economyResult{credit: c, update: u, failed: err != nil}
		}
	}
	refresh := func(now time.Time, events ...ctr.AchievementEvent) func(*ctr.EconomyStore) economyResult {
		return func(s *ctr.EconomyStore) economyResult {
			u, err := s.ReportPitStopRefresh(pid, events, now)
			return economyResult{update: u, failed: err != nil}
		}
	}
	steps := []step{
		{"purchase", purchase(200085)},
		{"owned retry", purchase(200085)},
		{"refused batch", purchase(200597, math.MaxUint32)},
		{"purchase batch", purchase(200597, 200085)},
	}
	// Buy one component first, then its bundle at the remaining component price.
	for id, sku := range ctr.EconomyCatalogSnapshot() {
		if len(sku.Parts) > 1 {
			steps = append(steps, step{"component", purchase(sku.Parts[0])}, step{"bundle", purchase(id)})
			break
		}
	}
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		e := capturedRace(at)
		e.Values["Online"], e.Values["Time"], e.Values["RewardGoldenWumpa"] = 1, 900, 1
		steps = append(steps, step{"online race and duplicate", race(at, e, e)}, step{"race replay", race(at, e)})
	}
	r := ruleForTest(t, "use_juiced_up_powerups_T_times")
	at := ruleTime(r)
	e := ruleEvent(r, at)
	steps = append(steps,
		step{"partial challenge", challenge(at, e, e, e, e)},
		step{"partial replay", challenge(at, e, e, e, e)})
	done := ruleEvent(r, at.Add(time.Second))
	done.Values["Progress"] = 6
	steps = append(steps, step{"challenge completion", challenge(at.Add(time.Second), done)}, step{"completion replay", challenge(at.Add(time.Second), done)})
	battle := ruleForTest(t, "win_game_in_T_different_battles_TRACKNAME")
	for i, mode := range []uint64{9, 9, 10, 11} {
		at := ruleTime(battle).Add(time.Duration(i) * time.Second)
		e := ruleEvent(battle, at)
		e.Values["X"] = mode
		steps = append(steps, step{"distinct battle modes", challenge(at, e)}, step{"battle replay", challenge(at, e)})
	}
	f := ctr.AchievementEvent{Name: "pitstop_daily_refresh", Timestamp: uint64(now.Unix()), Values: map[string]uint64{"value": 1}}
	steps = append(steps, step{"refresh", refresh(now, f, f)}, step{"refresh replay", refresh(now, f)})
	for i := 1; i <= 2; i++ {
		f.Timestamp++
		steps = append(steps, step{"refresh limit", refresh(now.Add(3*time.Second), f)})
	}
	steps = append(steps, step{"stale refresh", refresh(now.Add(24*time.Hour), f)})
	f.Timestamp = uint64(now.Add(24 * time.Hour).Unix())
	steps = append(steps, step{"next refresh day", refresh(now.Add(24*time.Hour), f)})
	parent := t
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			want, got := step.run(reference), step.run(s)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("result: got %+v, want %+v", got, want)
			}
			wantState, gotState := accountJSON(t, reference, pid), accountJSON(t, s, pid)
			if !bytes.Equal(gotState, wantState) {
				t.Fatalf("state: got %s, want %s", gotState, wantState)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db = openTestCTR(parent, filepath.Join(s.Dir, DBFile), false, testStartBalance)
			s.Backend = db
			reference = &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: reference.Dir}
			if got := accountJSON(t, s, pid); !bytes.Equal(got, wantState) {
				t.Fatalf("reopen: %s", got)
			}
		})
	}
	var storedPID int64
	if err := db.DB.QueryRow("SELECT pid FROM accounts").Scan(&storedPID); err != nil || storedPID != -1 {
		t.Fatalf("uint64 PID not stored as int64 bits: %d, %v", storedPID, err)
	}
}

func TestSQLitePurchaseAtomic(t *testing.T) {
	testStartBalance := int64(0)
	for _, balance := range []int64{0, 10000} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			testStartBalance = balance
			s, db := newTestEconomy(t, false, testStartBalance)
			for _, ids := range [][]uint32{{200085, math.MaxUint32}, {200085, 200597, math.MaxUint32}} {
				if _, err := s.Purchase(42, ids); err == nil {
					t.Fatal("invalid batch accepted")
				}
				if sqliteCount(t, db, "accounts") != 0 || sqliteCount(t, db, "ledger") != 0 || sqliteCount(t, db, "inventory") != 0 {
					t.Fatal("refused first purchase wrote state")
				}
			}
			if balance == 0 {
				if _, err := s.Purchase(42, []uint32{200085}); err == nil {
					t.Fatal("overspend accepted")
				}
				if sqliteCount(t, db, "accounts") != 0 || sqliteCount(t, db, "ledger") != 0 {
					t.Fatal("overspend wrote rows")
				}
			}
		})
	}
	testStartBalance = 10000
	for _, failure := range []struct{ name, sql string }{
		{"ledger", "CREATE TRIGGER fail_write BEFORE INSERT ON ledger WHEN NEW.item IS NOT NULL BEGIN SELECT RAISE(ABORT, 'test failure'); END"},
		{"balance", "CREATE TRIGGER fail_write BEFORE INSERT ON balances BEGIN SELECT RAISE(ABORT, 'test failure'); END"},
	} {
		for _, existing := range []bool{false, true} {
			t.Run(failure.name+map[bool]string{false: " new", true: " existing"}[existing], func(t *testing.T) {
				s, db := newTestEconomy(t, false, testStartBalance)
				var before []byte
				if existing {
					before = accountJSON(t, s, 42)
				}
				rows := sqliteCount(t, db, "ledger")
				sqliteExec(t, db, failure.sql)
				if _, err := s.Purchase(42, []uint32{200085}); err == nil {
					t.Fatal("failed commit returned success")
				}
				if sqliteCount(t, db, "ledger") != rows || sqliteCount(t, db, "inventory") != 0 {
					t.Fatal("partial debit/grant/ledger")
				}
				if !existing && sqliteCount(t, db, "accounts") != 0 {
					t.Fatal("partial account creation")
				}
				sqliteExec(t, db, "DROP TRIGGER fail_write")
				if existing && !bytes.Equal(before, accountJSON(t, s, 42)) {
					t.Fatal("failed purchase changed account")
				}
				if _, err := s.Purchase(42, []uint32{200085}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestSQLiteRewardAndRefreshAtomic(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 0
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	r := ruleForTest(t, "drift_for_X_seconds_consecutively")
	at := ruleTime(r)
	tests := []struct {
		table string
		run   func(*ctr.EconomyStore) error
	}{
		{"race_receipts", func(s *ctr.EconomyStore) error {
			e := capturedRace(now)
			e.Values["Online"] = 1
			_, err := s.AwardRaces(42, []ctr.AchievementEvent{e}, now)
			return err
		}},
		{"challenge_progress", func(s *ctr.EconomyStore) error {
			_, _, err := s.AwardChallenges(42, []ctr.AchievementEvent{ruleEvent(r, at)}, at)
			return err
		}},
		{"pit_stop_refresh", func(s *ctr.EconomyStore) error {
			e := ctr.AchievementEvent{Name: "pitstop_daily_refresh", Timestamp: uint64(now.Unix()), Values: map[string]uint64{"value": 1}}
			_, err := s.ReportPitStopRefresh(42, []ctr.AchievementEvent{e}, now)
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.table, func(t *testing.T) {
			s, db := newTestEconomy(t, false, testStartBalance)
			before := accountJSON(t, s, 42)
			sqliteExec(t, db, "CREATE TRIGGER fail_write BEFORE INSERT ON "+tc.table+" BEGIN SELECT RAISE(ABORT, 'test failure'); END")
			if err := tc.run(s); err == nil {
				t.Fatal("failed transaction accepted")
			}
			if !bytes.Equal(before, accountJSON(t, s, 42)) || sqliteCount(t, db, "ledger") != 0 {
				t.Fatal("partial reward or progress committed")
			}
			sqliteExec(t, db, "DROP TRIGGER fail_write")
			if err := tc.run(s); err != nil {
				t.Fatal(err)
			}
			after := accountJSON(t, s, 42)
			if bytes.Equal(before, after) {
				t.Fatal("retry did not apply")
			}
			rows := sqliteCount(t, db, "ledger")
			db.Close()
			db = openTestCTR(t, filepath.Join(s.Dir, DBFile), false, testStartBalance)
			s.Backend = db
			if err := tc.run(s); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, accountJSON(t, s, 42)) || sqliteCount(t, db, "ledger") != rows {
				t.Fatal("replay paid or advanced twice")
			}
		})
	}
}

func TestSQLiteLedgerAccountsForEveryChange(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 10000
	s, db := newTestEconomy(t, false, testStartBalance)
	const pid = uint64(42)
	if _, err := s.Purchase(pid, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	item := ctr.EconomyCatalogSnapshot()[200085].Items[0]
	var source string
	var stamp int64
	if err := db.DB.QueryRow("SELECT source, granted_at FROM inventory WHERE pid=? AND item=?", pid, item).Scan(&source, &stamp); err != nil || source != "purchase" || stamp <= 0 {
		t.Fatalf("grant provenance = %s %d, %v", source, stamp, err)
	}
	var delta int64
	var sku uint32
	var skus string
	if err := db.DB.QueryRow("SELECT delta, sku, skus FROM ledger WHERE source='purchase' AND currency=10").Scan(&delta, &sku, &skus); err != nil || delta != -ctr.EconomyCatalogSnapshot()[200085].Price || sku != 200085 || skus != "[200085]" {
		t.Fatalf("purchase audit = %d %d %s, %v", delta, sku, skus, err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	e1, e2 := capturedRace(now), capturedRace(now.Add(time.Second))
	if c, err := s.AwardRaces(pid, []ctr.AchievementEvent{e1, e2, e1}, now); err != nil || c != 54 {
		t.Fatalf("race credit %d, %v", c, err)
	}
	r := ruleForTest(t, "drift_for_X_seconds_consecutively")
	at := ruleTime(r)
	e := ruleEvent(r, at)
	if c, _, err := s.AwardChallenges(pid, []ctr.AchievementEvent{e}, at); err != nil || c != r.Reward {
		t.Fatalf("challenge credit %d, %v", c, err)
	}
	for _, tc := range []struct {
		source   string
		delta    int64
		receipts int
	}{
		{"starting balance", 10000, 0}, {"race reward", 54, 2}, {"challenge reward", r.Reward, 1},
	} {
		var delta int64
		var receipt sql.NullString
		if err := db.DB.QueryRow("SELECT delta, receipt FROM ledger WHERE source=? AND currency=10", tc.source).Scan(&delta, &receipt); err != nil {
			t.Fatal(err)
		}
		var refs []string
		if receipt.Valid {
			if err := json.Unmarshal([]byte(receipt.String), &refs); err != nil {
				t.Fatal(err)
			}
		}
		if delta != tc.delta || len(refs) != tc.receipts {
			t.Fatalf("%s: delta %d refs %v", tc.source, delta, refs)
		}
		if tc.source == "race reward" {
			a, err := s.Snapshot(pid)
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range refs {
				if !a.RaceReceipts[ref] {
					t.Fatalf("unknown race receipt %s", ref)
				}
			}
		}
		if tc.source == "challenge reward" {
			state, _, _ := r.Current(&ctr.EconomyAccount{}, at)
			if refs[0] != ctr.ChallengeKey(state) {
				t.Fatal("wrong completion reference")
			}
		}
	}
	// A second currency and an explicit grant exercise the generic delta seam.
	a, err := s.Snapshot(pid)
	if err != nil {
		t.Fatal(err)
	}
	a.Balances[11] = 100
	a.Items[math.MaxUint32] = true
	if err := s.SaveChange(pid, a, ctr.EconomyChange{Source: "manual grant"}); err != nil {
		t.Fatal(err)
	}
	var unlogged int
	if err := db.DB.QueryRow(`SELECT count(*) FROM inventory i WHERE NOT EXISTS (
		SELECT 1 FROM ledger l WHERE l.pid=i.pid AND l.item=i.item AND l.source=i.source AND l.timestamp=i.granted_at)`).Scan(&unlogged); err != nil || unlogged != 0 {
		t.Fatalf("unlogged items: %d, %v", unlogged, err)
	}
	a, err = s.Snapshot(pid)
	if err != nil {
		t.Fatal(err)
	}
	for currency, amount := range a.Balances {
		var sum int64
		if err := db.DB.QueryRow("SELECT coalesce(sum(delta),0) FROM ledger WHERE pid=? AND currency=?", pid, currency).Scan(&sum); err != nil || sum != amount {
			t.Fatalf("currency %d: ledger %d balance %d, %v", currency, sum, amount, err)
		}
	}
	if got := sqliteCount(t, db, "ledger"); got != 6+len(ctr.EconomyCatalogSnapshot()[200085].Items) {
		t.Fatalf("unexpected ledger rows: %d", got)
	}
	before := sqliteCount(t, db, "ledger")
	if _, err := s.Purchase(pid, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Purchase(pid, []uint32{1}); err == nil {
		t.Fatal("unknown SKU accepted")
	}
	if _, err := s.AwardRaces(pid, []ctr.AchievementEvent{e1, e2}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AwardChallenges(pid, []ctr.AchievementEvent{e}, at); err != nil {
		t.Fatal(err)
	}
	refresh := ctr.AchievementEvent{Name: "pitstop_daily_refresh", Timestamp: uint64(now.Unix()), Values: map[string]uint64{"value": 1}}
	if _, err := s.ReportPitStopRefresh(pid, []ctr.AchievementEvent{refresh}, now); err != nil {
		t.Fatal(err)
	}
	if got := sqliteCount(t, db, "ledger"); got != before {
		t.Fatalf("no balance/item change but ledger grew: %d -> %d", before, got)
	}
}

func TestSQLiteInventoryAndLedgerAreAppendOnly(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 10000
	s, db := newTestEconomy(t, false, testStartBalance)
	if _, err := s.Purchase(42, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	before := accountJSON(t, s, 42)
	n := sqliteCount(t, db, "ledger")
	for _, query := range []string{
		"UPDATE inventory SET source='changed'", "DELETE FROM inventory",
		"INSERT OR REPLACE INTO inventory SELECT pid, item, 'changed', granted_at FROM inventory",
		"UPDATE ledger SET delta=1", "DELETE FROM ledger",
		"INSERT OR REPLACE INTO ledger SELECT * FROM ledger",
	} {
		if _, err := db.DB.Exec(query); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	a, err := s.Snapshot(42)
	if err != nil {
		t.Fatal(err)
	}
	for item := range a.Items {
		delete(a.Items, item)
		break
	}
	a.Balances[10]++
	if err := s.Save(42, a); err == nil {
		t.Fatal("revocation accepted")
	}
	if !bytes.Equal(before, accountJSON(t, s, 42)) || sqliteCount(t, db, "ledger") != n {
		t.Fatal("refused revocation changed state")
	}
}

func TestSQLiteConcurrentPurchaseRetry(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 10000
	s, db := newTestEconomy(t, false, testStartBalance)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Purchase(42, []uint32{200085}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	a, err := s.Snapshot(42)
	if err != nil || a.Balances[10] != 10000-ctr.EconomyCatalogSnapshot()[200085].Price {
		t.Fatalf("concurrent purchase: %+v, %v", a, err)
	}
	if sqliteCount(t, db, "ledger") != 2+len(a.Items) {
		t.Fatal("concurrent purchase logged twice")
	}
}

func TestSQLiteModeSchemaAndDurabilitySettings(t *testing.T) {
	testStartBalance := int64(0)
	for _, unlockAll := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[unlockAll], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), DBFile)
			db := openTestCTR(t, path, unlockAll, testStartBalance)
			var schema, mode, journal string
			var version, synchronous, timeout int
			if err := db.DB.QueryRow("SELECT sql FROM sqlite_master WHERE name='meta'").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			if schema != "CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)" {
				t.Fatalf("meta schema: %s", schema)
			}
			if err := db.DB.QueryRow("SELECT value FROM meta WHERE key='unlock_all'").Scan(&mode); err != nil {
				t.Fatal(err)
			}
			var trust string
			if err := db.DB.QueryRow("SELECT value FROM meta WHERE key='trust_id'").Scan(&trust); err != nil {
				t.Fatal(err)
			}
			if mode != map[bool]string{false: "0", true: "1"}[unlockAll] || trust != testTrustID || sqliteCount(t, db, "meta") != 2 {
				t.Fatalf("meta rows: %q %q", mode, trust)
			}
			if err := db.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := db.DB.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
				t.Fatal(err)
			}
			if err := db.DB.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
				t.Fatal(err)
			}
			if err := db.DB.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
				t.Fatal(err)
			}
			if version != SchemaVersion || journal != "wal" || synchronous != 2 || timeout != 5000 || db.DB.Stats().MaxOpenConnections != 1 {
				t.Fatalf("settings: %d %s %d %d", version, journal, synchronous, timeout)
			}
			db.Close()
			db = openTestCTR(t, path, unlockAll, testStartBalance)
			db.Close()
			if other, err := OpenSQLite(path, !unlockAll, testTrustID); err == nil {
				other.Close()
				t.Fatal("mode mismatch accepted")
			}
			db = openTestCTR(t, path, unlockAll, testStartBalance)
			sqliteExec(t, db, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion+1))
			db.Close()
			if newer, err := OpenSQLite(path, unlockAll, testTrustID); err == nil {
				newer.Close()
				t.Fatal("newer schema accepted")
			}
		})
	}
	for _, statement := range []string{"DELETE FROM meta", "UPDATE meta SET value='invalid'"} {
		t.Run(statement, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), DBFile)
			db := openTestCTR(t, path, false, testStartBalance)
			sqliteExec(t, db, statement)
			db.Close()
			if reopened, err := OpenSQLite(path, false, testTrustID); err == nil {
				reopened.Close()
				t.Fatal("missing/invalid mode accepted")
			}
		})
	}
}

func TestSQLiteStartsFreshAndFixesStartingBalance(t *testing.T) {
	testStartBalance := int64(0)
	testStartBalance = 321
	s, db := newTestEconomy(t, false, testStartBalance)
	// Corrupt reference files must neither be parsed nor replaced by SQLite.
	files := []string{"42.json", "42.items.json", "42.economy-v2.json"}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(s.Dir, file), []byte("not JSON"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.Snapshot(42)
	if err != nil || a.Balances[10] != 321 || len(a.Items) != 0 {
		t.Fatalf("fresh account: %+v, %v", a, err)
	}
	testStartBalance = 999
	db.Close()
	db = openTestCTR(t, filepath.Join(s.Dir, DBFile), false, testStartBalance)
	s.Backend = db
	a, err = s.Snapshot(42)
	if err != nil || a.Balances[10] != 321 {
		t.Fatalf("existing balance: %+v, %v", a, err)
	}
	a, err = s.Snapshot(43)
	if err != nil || a.Balances[10] != 999 {
		t.Fatalf("new balance: %+v, %v", a, err)
	}
	for _, file := range files {
		b, err := os.ReadFile(filepath.Join(s.Dir, file))
		if err != nil || string(b) != "not JSON" {
			t.Fatal("reference file changed")
		}
	}
}

func TestSQLiteRefusesSecondInstance(t *testing.T) {
	testStartBalance := int64(0)
	path := filepath.Join(t.TempDir(), DBFile)
	db := openTestCTR(t, path, false, testStartBalance)
	if other, err := OpenSQLite(path, false, testTrustID); err == nil {
		other.Close()
		t.Fatal("second instance opened the same database")
	}
	db.Close()
	reopened := openTestCTR(t, path, false, testStartBalance)
	reopened.Close()
}

func TestGuestNamespaceCannotReuseVerifiedAccountDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), DBFile)
	db, err := OpenSQLite(path, true, testTrustID)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if guestValue, err := OpenSQLite(path, true, guest.TrustID); err == nil {
		guestValue.Close()
		t.Fatal("guest instance reused verified account state")
	}
	path = filepath.Join(t.TempDir(), DBFile)
	db, err = OpenSQLite(path, true, guest.TrustID)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if signed, err := OpenSQLite(path, true, testTrustID); err == nil {
		signed.Close()
		t.Fatal("verified instance reused anonymous state")
	}
}

func TestPlayerNamesSurviveRestart(t *testing.T) {
	testStartBalance := int64(0)
	path := filepath.Join(t.TempDir(), DBFile)
	db := openTestCTR(t, path, false, testStartBalance)
	at := time.Now().Add(-time.Hour)
	for _, r := range []struct {
		pid  uint64
		name string
		at   time.Time
	}{{9, "Later", at}, {9, "Renamed", at.Add(time.Minute)}, {10, "Later", at.Add(2 * time.Minute)}} {
		if err := db.RememberName(r.pid, r.name, r.at); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db = openTestCTR(t, path, false, testStartBalance)
	got, err := db.NamedPlayers([]string{"LATER", "renamed", "stranger"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["later"] == nil || got["later"].PID != 10 || got["renamed"] == nil || got["renamed"].PID != 9 || got["renamed"].Username != "Renamed" {
		t.Fatalf("names after restart: %+v", got)
	}
}

func TestCTRSchemaRefusesPreReleaseVersions(t *testing.T) {
	testStartBalance := int64(0)
	for _, old := range []int{2, 3} {
		path := filepath.Join(t.TempDir(), DBFile)
		db := openTestCTR(t, path, false, testStartBalance)
		sqliteExec(t, db, fmt.Sprintf("PRAGMA user_version = %d", old))
		if err := db.RememberName(9, "Fixture", time.Now()); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if other, err := OpenSQLite(path, false, testTrustID); err == nil {
			other.Close()
			t.Fatalf("pre-release schema %d accepted", old)
		}
		// A refused open must leave the old database recoverable.
		check, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { check.Close() })
		var version int
		if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != old {
			t.Fatalf("version %d changed to %d, %v", old, version, err)
		}
		var name string
		if err := check.QueryRow("SELECT name FROM player_names WHERE pid = 9").Scan(&name); err != nil || name != "Fixture" {
			t.Fatalf("refused database lost its name: %q, %v", name, err)
		}
	}
}

func TestCTRDatabaseRefusesAnotherTrust(t *testing.T) {
	testStartBalance := int64(0)
	path := filepath.Join(t.TempDir(), DBFile)
	openTestCTR(t, path, false, testStartBalance).Close()
	if other, err := OpenSQLite(path, false, "another-issuer"); err == nil {
		other.Close()
		t.Fatal("database opened for another shadNet issuer")
	}
	if _, err := OpenSQLite(filepath.Join(t.TempDir(), DBFile), false, ""); err == nil {
		t.Fatal("database opened without a trust ID")
	}
	for _, statement := range []string{"DELETE FROM meta WHERE key = 'trust_id'", "UPDATE meta SET value = 'x' WHERE key = 'trust_id'"} {
		db := openTestCTR(t, path, false, testStartBalance)
		sqliteExec(t, db, statement)
		db.Close()
		if reopened, err := OpenSQLite(path, false, testTrustID); err == nil {
			reopened.Close()
			t.Fatalf("%s accepted", statement)
		}
		path = filepath.Join(t.TempDir(), DBFile)
		openTestCTR(t, path, false, testStartBalance).Close()
	}
}

func newTestEconomy(t *testing.T, unlockAll bool, initialBalance int64) (*ctr.EconomyStore, *SQLite) {
	t.Helper()
	dir := t.TempDir()
	db := openTestCTR(t, filepath.Join(dir, DBFile), unlockAll, initialBalance)
	return &ctr.EconomyStore{InitialBalance: func() int64 { return initialBalance }, Dir: dir, Backend: db, UnlockAll: unlockAll}, db
}
