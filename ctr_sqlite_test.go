package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func openTestCTR(t *testing.T, path string, unlockAll bool) *ctrSQLite {
	t.Helper()
	db, err := openCTRSQLite(path, unlockAll)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestEconomy(t *testing.T, unlockAll bool) (*economyStore, *ctrSQLite) {
	t.Helper()
	dir := t.TempDir()
	db := openTestCTR(t, filepath.Join(dir, ctrDB), unlockAll)
	return &economyStore{dir: dir, backend: db, unlockAll: unlockAll}, db
}

func sqliteCount(t *testing.T, db *ctrSQLite, table string) int {
	t.Helper()
	var n int
	if err := db.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sqliteChanges(t *testing.T, db *ctrSQLite) int {
	t.Helper()
	var n int
	if err := db.db.QueryRow("SELECT total_changes()").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sqliteExec(t *testing.T, db *ctrSQLite, query string, args ...any) {
	t.Helper()
	if _, err := db.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func accountJSON(t *testing.T, s *economyStore, pid uint64) []byte {
	t.Helper()
	a, err := s.snapshot(pid)
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
	t.Setenv("CTR_START_BALANCE", "20000")
	reference := &economyStore{dir: t.TempDir()}
	s, db := newTestEconomy(t, false)
	const pid = uint64(math.MaxUint64)
	type step struct {
		name string
		run  func(*economyStore) economyResult
	}
	purchase := func(ids ...uint32) func(*economyStore) economyResult {
		return func(s *economyStore) economyResult {
			c, err := s.purchase(pid, ids)
			return economyResult{credit: c, failed: err != nil}
		}
	}
	race := func(now time.Time, events ...achievementEvent) func(*economyStore) economyResult {
		return func(s *economyStore) economyResult {
			c, err := s.awardRaces(pid, events, now)
			return economyResult{credit: c, failed: err != nil}
		}
	}
	challenge := func(now time.Time, events ...achievementEvent) func(*economyStore) economyResult {
		return func(s *economyStore) economyResult {
			c, u, err := s.awardChallenges(pid, events, now)
			return economyResult{credit: c, update: u, failed: err != nil}
		}
	}
	refresh := func(now time.Time, events ...achievementEvent) func(*economyStore) economyResult {
		return func(s *economyStore) economyResult {
			u, err := s.reportPitStopRefresh(pid, events, now)
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
	for id, sku := range economyCatalog {
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
	f := achievementEvent{Name: "pitstop_daily_refresh", Timestamp: uint64(now.Unix()), Values: map[string]uint64{"value": 1}}
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
			db = openTestCTR(parent, filepath.Join(s.dir, ctrDB), false)
			s.backend = db
			reference = &economyStore{dir: reference.dir}
			if got := accountJSON(t, s, pid); !bytes.Equal(got, wantState) {
				t.Fatalf("reopen: %s", got)
			}
		})
	}
	var storedPID int64
	if err := db.db.QueryRow("SELECT pid FROM accounts").Scan(&storedPID); err != nil || storedPID != -1 {
		t.Fatalf("uint64 PID not stored as int64 bits: %d, %v", storedPID, err)
	}
}

func TestSQLitePurchaseAtomic(t *testing.T) {
	for _, balance := range []string{"0", "10000"} {
		t.Run(balance, func(t *testing.T) {
			t.Setenv("CTR_START_BALANCE", balance)
			s, db := newTestEconomy(t, false)
			for _, ids := range [][]uint32{{200085, math.MaxUint32}, {200085, 200597, math.MaxUint32}} {
				if _, err := s.purchase(42, ids); err == nil {
					t.Fatal("invalid batch accepted")
				}
				if sqliteCount(t, db, "accounts") != 0 || sqliteCount(t, db, "ledger") != 0 || sqliteCount(t, db, "inventory") != 0 {
					t.Fatal("refused first purchase wrote state")
				}
			}
			if balance == "0" {
				if _, err := s.purchase(42, []uint32{200085}); err == nil {
					t.Fatal("overspend accepted")
				}
				if sqliteCount(t, db, "accounts") != 0 || sqliteCount(t, db, "ledger") != 0 {
					t.Fatal("overspend wrote rows")
				}
			}
		})
	}
	t.Setenv("CTR_START_BALANCE", "10000")
	for _, failure := range []struct{ name, sql string }{
		{"ledger", "CREATE TRIGGER fail_write BEFORE INSERT ON ledger WHEN NEW.item IS NOT NULL BEGIN SELECT RAISE(ABORT, 'test failure'); END"},
		{"balance", "CREATE TRIGGER fail_write BEFORE INSERT ON balances BEGIN SELECT RAISE(ABORT, 'test failure'); END"},
	} {
		for _, existing := range []bool{false, true} {
			t.Run(failure.name+map[bool]string{false: " new", true: " existing"}[existing], func(t *testing.T) {
				s, db := newTestEconomy(t, false)
				var before []byte
				if existing {
					before = accountJSON(t, s, 42)
				}
				rows := sqliteCount(t, db, "ledger")
				sqliteExec(t, db, failure.sql)
				if _, err := s.purchase(42, []uint32{200085}); err == nil {
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
				if _, err := s.purchase(42, []uint32{200085}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestSQLiteRewardAndRefreshAtomic(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "0")
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	r := ruleForTest(t, "drift_for_X_seconds_consecutively")
	at := ruleTime(r)
	tests := []struct {
		table string
		run   func(*economyStore) error
	}{
		{"race_receipts", func(s *economyStore) error {
			e := capturedRace(now)
			e.Values["Online"] = 1
			_, err := s.awardRaces(42, []achievementEvent{e}, now)
			return err
		}},
		{"challenge_progress", func(s *economyStore) error {
			_, _, err := s.awardChallenges(42, []achievementEvent{ruleEvent(r, at)}, at)
			return err
		}},
		{"pit_stop_refresh", func(s *economyStore) error {
			e := achievementEvent{Name: "pitstop_daily_refresh", Timestamp: uint64(now.Unix()), Values: map[string]uint64{"value": 1}}
			_, err := s.reportPitStopRefresh(42, []achievementEvent{e}, now)
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.table, func(t *testing.T) {
			s, db := newTestEconomy(t, false)
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
			db = openTestCTR(t, filepath.Join(s.dir, ctrDB), false)
			s.backend = db
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
	t.Setenv("CTR_START_BALANCE", "10000")
	s, db := newTestEconomy(t, false)
	const pid = uint64(42)
	if _, err := s.purchase(pid, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	item := economyCatalog[200085].Items[0]
	var source string
	var stamp int64
	if err := db.db.QueryRow("SELECT source, granted_at FROM inventory WHERE pid=? AND item=?", pid, item).Scan(&source, &stamp); err != nil || source != "purchase" || stamp <= 0 {
		t.Fatalf("grant provenance = %s %d, %v", source, stamp, err)
	}
	var delta int64
	var sku uint32
	var skus string
	if err := db.db.QueryRow("SELECT delta, sku, skus FROM ledger WHERE source='purchase' AND currency=10").Scan(&delta, &sku, &skus); err != nil || delta != -economyCatalog[200085].Price || sku != 200085 || skus != "[200085]" {
		t.Fatalf("purchase audit = %d %d %s, %v", delta, sku, skus, err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	e1, e2 := capturedRace(now), capturedRace(now.Add(time.Second))
	if c, err := s.awardRaces(pid, []achievementEvent{e1, e2, e1}, now); err != nil || c != 54 {
		t.Fatalf("race credit %d, %v", c, err)
	}
	r := ruleForTest(t, "drift_for_X_seconds_consecutively")
	at := ruleTime(r)
	e := ruleEvent(r, at)
	if c, _, err := s.awardChallenges(pid, []achievementEvent{e}, at); err != nil || c != r.Reward {
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
		if err := db.db.QueryRow("SELECT delta, receipt FROM ledger WHERE source=? AND currency=10", tc.source).Scan(&delta, &receipt); err != nil {
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
			a, err := s.snapshot(pid)
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
			state, _, _ := r.current(&economyAccount{}, at)
			if refs[0] != challengeKey(state) {
				t.Fatal("wrong completion reference")
			}
		}
	}
	// A second currency and an explicit grant exercise the generic delta seam.
	a, err := s.snapshot(pid)
	if err != nil {
		t.Fatal(err)
	}
	a.Balances[11] = 100
	a.Items[math.MaxUint32] = true
	if err := s.saveChange(pid, a, economyChange{source: "manual grant"}); err != nil {
		t.Fatal(err)
	}
	var unlogged int
	if err := db.db.QueryRow(`SELECT count(*) FROM inventory i WHERE NOT EXISTS (
		SELECT 1 FROM ledger l WHERE l.pid=i.pid AND l.item=i.item AND l.source=i.source AND l.timestamp=i.granted_at)`).Scan(&unlogged); err != nil || unlogged != 0 {
		t.Fatalf("unlogged items: %d, %v", unlogged, err)
	}
	a, err = s.snapshot(pid)
	if err != nil {
		t.Fatal(err)
	}
	for currency, amount := range a.Balances {
		var sum int64
		if err := db.db.QueryRow("SELECT coalesce(sum(delta),0) FROM ledger WHERE pid=? AND currency=?", pid, currency).Scan(&sum); err != nil || sum != amount {
			t.Fatalf("currency %d: ledger %d balance %d, %v", currency, sum, amount, err)
		}
	}
	if got := sqliteCount(t, db, "ledger"); got != 6+len(economyCatalog[200085].Items) {
		t.Fatalf("unexpected ledger rows: %d", got)
	}
	before := sqliteCount(t, db, "ledger")
	if _, err := s.purchase(pid, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.purchase(pid, []uint32{1}); err == nil {
		t.Fatal("unknown SKU accepted")
	}
	if _, err := s.awardRaces(pid, []achievementEvent{e1, e2}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.awardChallenges(pid, []achievementEvent{e}, at); err != nil {
		t.Fatal(err)
	}
	refresh := achievementEvent{Name: "pitstop_daily_refresh", Timestamp: uint64(now.Unix()), Values: map[string]uint64{"value": 1}}
	if _, err := s.reportPitStopRefresh(pid, []achievementEvent{refresh}, now); err != nil {
		t.Fatal(err)
	}
	if got := sqliteCount(t, db, "ledger"); got != before {
		t.Fatalf("no balance/item change but ledger grew: %d -> %d", before, got)
	}
}

func TestSQLiteInventoryAndLedgerAreAppendOnly(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "10000")
	s, db := newTestEconomy(t, false)
	if _, err := s.purchase(42, []uint32{200085}); err != nil {
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
		if _, err := db.db.Exec(query); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	a, err := s.snapshot(42)
	if err != nil {
		t.Fatal(err)
	}
	for item := range a.Items {
		delete(a.Items, item)
		break
	}
	a.Balances[10]++
	if err := s.save(42, a); err == nil {
		t.Fatal("revocation accepted")
	}
	if !bytes.Equal(before, accountJSON(t, s, 42)) || sqliteCount(t, db, "ledger") != n {
		t.Fatal("refused revocation changed state")
	}
}

func TestSQLiteConcurrentPurchaseRetry(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "10000")
	s, db := newTestEconomy(t, false)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.purchase(42, []uint32{200085}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	a, err := s.snapshot(42)
	if err != nil || a.Balances[10] != 10000-economyCatalog[200085].Price {
		t.Fatalf("concurrent purchase: %+v, %v", a, err)
	}
	if sqliteCount(t, db, "ledger") != 2+len(a.Items) {
		t.Fatal("concurrent purchase logged twice")
	}
}

func TestSQLiteModeSchemaAndDurabilitySettings(t *testing.T) {
	for _, unlockAll := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[unlockAll], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ctrDB)
			db := openTestCTR(t, path, unlockAll)
			var schema, mode, journal string
			var version, synchronous, timeout int
			if err := db.db.QueryRow("SELECT sql FROM sqlite_master WHERE name='meta'").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			if schema != "CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)" {
				t.Fatalf("meta schema: %s", schema)
			}
			if err := db.db.QueryRow("SELECT value FROM meta WHERE key='unlock_all'").Scan(&mode); err != nil {
				t.Fatal(err)
			}
			if mode != map[bool]string{false: "0", true: "1"}[unlockAll] || sqliteCount(t, db, "meta") != 1 {
				t.Fatalf("meta row: %q", mode)
			}
			if err := db.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := db.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
				t.Fatal(err)
			}
			if err := db.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
				t.Fatal(err)
			}
			if err := db.db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
				t.Fatal(err)
			}
			if version != ctrSchemaVersion || journal != "wal" || synchronous != 2 || timeout != 5000 || db.db.Stats().MaxOpenConnections != 1 {
				t.Fatalf("settings: %d %s %d %d", version, journal, synchronous, timeout)
			}
			db.Close()
			db = openTestCTR(t, path, unlockAll)
			db.Close()
			if other, err := openCTRSQLite(path, !unlockAll); err == nil {
				other.Close()
				t.Fatal("mode mismatch accepted")
			}
			db = openTestCTR(t, path, unlockAll)
			sqliteExec(t, db, fmt.Sprintf("PRAGMA user_version = %d", ctrSchemaVersion+1))
			db.Close()
			if newer, err := openCTRSQLite(path, unlockAll); err == nil {
				newer.Close()
				t.Fatal("newer schema accepted")
			}
		})
	}
	for _, statement := range []string{"DELETE FROM meta", "UPDATE meta SET value='invalid'"} {
		t.Run(statement, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ctrDB)
			db := openTestCTR(t, path, false)
			sqliteExec(t, db, statement)
			db.Close()
			if reopened, err := openCTRSQLite(path, false); err == nil {
				reopened.Close()
				t.Fatal("missing/invalid mode accepted")
			}
		})
	}
}

func TestSQLiteStartsFreshAndFixesStartingBalance(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "321")
	s, db := newTestEconomy(t, false)
	// Corrupt reference files must neither be parsed nor replaced by SQLite.
	files := []string{"42.json", "42.items.json", "42.economy-v2.json"}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(s.dir, file), []byte("not JSON"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.snapshot(42)
	if err != nil || a.Balances[10] != 321 || len(a.Items) != 0 {
		t.Fatalf("fresh account: %+v, %v", a, err)
	}
	t.Setenv("CTR_START_BALANCE", "999")
	db.Close()
	db = openTestCTR(t, filepath.Join(s.dir, ctrDB), false)
	s.backend = db
	a, err = s.snapshot(42)
	if err != nil || a.Balances[10] != 321 {
		t.Fatalf("existing balance: %+v, %v", a, err)
	}
	a, err = s.snapshot(43)
	if err != nil || a.Balances[10] != 999 {
		t.Fatalf("new balance: %+v, %v", a, err)
	}
	for _, file := range files {
		b, err := os.ReadFile(filepath.Join(s.dir, file))
		if err != nil || string(b) != "not JSON" {
			t.Fatal("reference file changed")
		}
	}
}
