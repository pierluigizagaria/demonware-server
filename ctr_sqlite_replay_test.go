package main

import (
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSQLiteReplayStateRetention(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "0")
	s, db := newTestEconomy(t, false)
	now := time.Now().UTC()
	first := capturedRace(now)
	first.Values["Online"] = 1
	if amount, err := s.awardRaces(42, []achievementEvent{first}, now); err != nil || amount == 0 {
		t.Fatalf("first race: %d, %v", amount, err)
	}
	// Age the stored replay state beyond the retention window.
	old := now.Add(-replayRetention - time.Hour).Unix()
	sqliteExec(t, db, `UPDATE race_receipts SET recorded_at = ?`, old)
	sqliteExec(t, db, `UPDATE online_seconds SET updated_at = ?`, old)
	if a, err := s.snapshot(42); err != nil || len(a.RaceReceipts) != 0 || len(a.OnlineSeconds) != 0 {
		t.Fatalf("expired replay state loaded: %+v, %v", a, err)
	}
	// A receipt still stored but outside the window is not inserted twice.
	if _, err := s.awardRaces(42, []achievementEvent{first}, now); err != nil {
		t.Fatal(err)
	}
	second := capturedRace(now.Add(-time.Minute))
	if _, err := s.awardRaces(42, []achievementEvent{second}, now); err != nil {
		t.Fatal(err)
	}
	var expired int
	if err := db.db.QueryRow(`SELECT (SELECT count(*) FROM race_receipts WHERE recorded_at < ?1) +
		(SELECT count(*) FROM online_seconds WHERE updated_at < ?1)`, now.Add(-replayRetention).Unix()).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if expired != 0 || sqliteCount(t, db, "race_receipts") != 1 {
		t.Fatalf("expired replay state kept: %d expired, %d receipts", expired, sqliteCount(t, db, "race_receipts"))
	}
	if a, err := s.snapshot(42); err != nil || len(a.RaceReceipts) != 1 {
		t.Fatalf("current receipt lost: %+v, %v", a, err)
	}
}

func TestSQLiteSaveWritesOnlyChangedRows(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "1000")
	s, db := newTestEconomy(t, false)
	now := time.Now().UTC()
	if _, err := s.awardRaces(42, []achievementEvent{capturedRace(now)}, now); err != nil {
		t.Fatal(err)
	}
	before := sqliteChanges(t, db)
	if _, err := s.awardRaces(42, []achievementEvent{capturedRace(now.Add(-time.Minute))}, now); err != nil {
		t.Fatal(err)
	}
	// One ledger row, one balance update, one receipt; nothing rewritten.
	if changed := sqliteChanges(t, db) - before; changed != 3 {
		t.Fatalf("reward changed %d rows", changed)
	}
}

func TestSQLiteRankedReadsMatchReference(t *testing.T) {
	json := &ctrStatsStore{dir: t.TempDir()}
	db := openTestLeaderboard(t, filepath.Join(t.TempDir(), ctrDB))
	var writes []ctrStatWrite
	for i, pid := range []uint64{5, 1 << 63, 7, math.MaxUint64, 2, 9, 1<<63 + 4, 11} {
		writes = append(writes, ctrStatWrite{ctrStat: ctrStat{Board: 89, PID: pid, Score: int64(i % 3), Name: "p"}, Operation: 0})
	}
	for _, store := range []leaderboardStore{json, db} {
		if err := store.write(writes); err != nil {
			t.Fatal(err)
		}
	}
	for _, page := range [][2]uint64{{0, 3}, {1, 8}, {3, 2}, {8, 5}, {9, 5}, {math.MaxUint64, 1}, {2, 0}} {
		want, wantTotal, err1 := readLeaderboard(json, 4, 89, page[0], uint32(page[1]), nil)
		got, gotTotal, err2 := readLeaderboard(db, 4, 89, page[0], uint32(page[1]), nil)
		// The handler maps start 0 to 1 before reading.
		if page[0] == 0 {
			want, wantTotal, err1 = readLeaderboard(json, 4, 89, 1, uint32(page[1]), nil)
		}
		if err1 != nil || err2 != nil || gotTotal != wantTotal || !reflect.DeepEqual(got, want) {
			t.Fatalf("page %v: sqlite %+v/%d, reference %+v/%d (%v, %v)", page, got, gotTotal, want, wantTotal, err1, err2)
		}
	}
	ids := map[uint64]bool{math.MaxUint64: true, 2: true, 1 << 63: true, 404: true}
	want, wantTotal, err1 := readLeaderboard(json, 13, 89, 0, 0, ids)
	got, gotTotal, err2 := readLeaderboard(db, 13, 89, 0, 0, ids)
	if err1 != nil || err2 != nil || gotTotal != wantTotal || !reflect.DeepEqual(got, want) {
		t.Fatalf("entities: sqlite %+v/%d, reference %+v/%d", got, gotTotal, want, wantTotal)
	}
	var plan strings.Builder
	rs, err := db.db.Query(`EXPLAIN QUERY PLAN SELECT pid FROM leaderboard WHERE board = ? ORDER BY `+leaderboardOrder+` LIMIT 3 OFFSET 2`, 89)
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
