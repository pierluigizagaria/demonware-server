package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

func testCTRSearchFor(m *ctr.Matchmaker, t *testing.T, pid uint64, playlist int) *ctr.Search {
	t.Helper()
	info := ctr.PlayerInfo{}
	info.Listen.Address = base64.StdEncoding.EncodeToString(make([]byte, 67))
	info.Listen.ID = base64.StdEncoding.EncodeToString(make([]byte, 8))
	info.Listen.Key = base64.StdEncoding.EncodeToString(make([]byte, 16))
	raw, _ := json.Marshal(info)
	l := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: testSearchConns.Add(1), Player: &platform.PlayerID{PID: pid}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: m, registry: testRegistry, ctr: testCTR, lobbyDoc: string(raw)})
	search, err := m.ParseSearch(l, fmt.Sprintf(`{"members":[%d],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":%d,"matchmaking_version":200}}}`, pid, playlist), l.lobbyDoc)
	if err != nil {
		t.Fatal(err)
	}
	return search
}

func testCTRSearch(t *testing.T, pid uint64, playlist int) *ctr.Search {
	return testCTRSearchFor(testMatchmaker, t, pid, playlist)
}

var testSearchConns atomic.Uint64
