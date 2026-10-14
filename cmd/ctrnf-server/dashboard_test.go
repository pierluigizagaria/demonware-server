package main

import (
	"encoding/json"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

func TestDashboardCTRMembershipAndTitleSplit(t *testing.T) {
	oldPresence := testPresence
	oldTelemetry := testTelemetry
	oldRooms := testMatchmaker.Rooms
	oldRegistry := testRegistry
	defer func() {
		testPresence = oldPresence
		testTelemetry = oldTelemetry
		testMatchmaker.Rooms = oldRooms
		testRegistry = oldRegistry
	}()
	players := map[uint64]*platform.PlayerID{1: {PID: 101, Username: "host", Title: 5760}, 2: {PID: 102, Username: "joined", Title: 5760}, 3: {PID: 103, Username: "pending", Title: 5760}, 4: {PID: 104, Username: "diablo"}}
	testTelemetry = dashboard.NewTelemetry(testConfig, func() int64 { return 0 })
	testPresence = presence.NewStore(nil)
	for id, player := range players {
		testPresence.RegisterOnline(id, player)
		testTelemetry.RegisterConn(id, "")
	}
	host := &ctr.Search{PID: 101, Doc: ctr.SearchDoc{Members: []uint64{101}}}
	guest := &ctr.Search{PID: 102, Doc: ctr.SearchDoc{Members: []uint64{102, 103}}}
	testMatchmaker.Rooms = map[uint64]*ctr.Room{88: {ID: 88, Host: host, Players: []*ctr.Search{host, guest}, Capacity: 8, HostDoc: map[string]any{"player_state": map[string]any{"101": json.Number("2"), "102": json.Number("2"), "103": json.Number("1")}}}}
	testRegistry = lobby.NewSessionRegistry()
	for _, id := range []uint64{1, 2} {
		peer := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: id, Player: players[id]}, lobbyDependencies{registry: testRegistry, relay: testRelay})
		info := &demonware.Writer{}
		info.Blob(make([]byte, 67))
		info.Uint32(8)
		info.Uint32(8)
		reply := peer.onMatchMakingContext(lobby.MMCreateSession, demonware.NewReader(info.Bytes()), "ctr:dashboard")
		if id == 1 {
			sessionID, err := demonware.NewReader(reply[26:]).Blob()
			if err != nil {
				t.Fatal(err)
			}
			update := &demonware.Writer{}
			update.Blob(sessionID)
			update.Uint32(2)
			update.Raw(info.Bytes())
			peer.onMatchMakingContext(lobby.MMUpdatePlayers, demonware.NewReader(update.Bytes()), "ctr:dashboard")
		}
	}
	s := testBuildGameStats("ctr")
	if s.Connected != 3 || s.ActiveLobbies != 1 || s.InLobby != 2 || s.Gatherings[0].Count != 2 {
		t.Fatalf("wrong counters: %+v", s)
	}
	for _, p := range s.Players {
		if p.PID == 102 && (p.IsHost || p.Gathering != 88) {
			t.Fatalf("guest misclassified %+v", p)
		}
		if p.PID == 103 && p.Gathering != 0 {
			t.Fatal("reservation counted as joined")
		}
	}
	d := testBuildGameStats("d3")
	if d.Connected != 1 || d.ActiveLobbies != 0 || d.Server.AccessKey != "demonware-d3" {
		t.Fatalf("title split %+v", d)
	}
}
