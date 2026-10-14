package main

import (
	"path/filepath"
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
