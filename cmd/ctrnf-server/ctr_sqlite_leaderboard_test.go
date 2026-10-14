package main

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

func openTestLeaderboard(t *testing.T, path string) *store.SQLite {
	t.Helper()
	s, err := store.OpenSQLite(path, false, testTrustID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The SQLite store must answer exactly like the upstream JSON store.
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
	sqlite := openTestLeaderboard(t, filepath.Join(t.TempDir(), store.DBFile))
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
	path := filepath.Join(t.TempDir(), store.DBFile)
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

func TestSQLiteLeaderboardServesStatsTasks(t *testing.T) {
	oldOnline, oldStore := testPresence.Online, testCTR.Stats
	defer func() { testPresence.Online = oldOnline; testCTR.Stats = oldStore }()
	testPresence.Online = map[uint64]*platform.PlayerID{9: {PID: 12, Username: "test", Title: 5760}}
	testCTR.Stats = openTestLeaderboard(t, filepath.Join(t.TempDir(), store.DBFile))
	l := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 9, Player: testPresence.Online[9]}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	if err := testCTR.Stats.Write([]ctr.StatWrite{{Stat: ctr.Stat{Board: 1001, PID: 12, Score: 400, Name: "A"}, Operation: 4}, {Stat: ctr.Stat{Board: 1001, PID: 13, Score: 500, Name: "B"}, Operation: 4}}); err != nil {
		t.Fatal(err)
	}
	w := &demonware.Writer{}
	w.Uint32(1001)
	w.Uint64(2)
	w.Uint32(1)
	w.Uint32(0)
	w.Raw([]byte{0})
	r := demonware.NewReader(l.onCTRStats(4, demonware.NewReader(w.Bytes())))
	r.Uint64()
	code, _ := r.Uint32()
	r.Uint8()
	n, _ := r.Uint32()
	total, _ := r.Uint32()
	pid, _ := r.Uint64()
	if code != 0 || n != 1 || total != 2 || pid != 12 {
		t.Fatalf("rank read = code %d rows %d total %d pid %d", code, n, total, pid)
	}
}
