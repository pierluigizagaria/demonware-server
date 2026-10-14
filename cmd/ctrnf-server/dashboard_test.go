package main

import (
	"encoding/json"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

func TestDashboardCTRMembershipAndTitleSplit(t *testing.T) {
	oldOnline := testPresence.Online
	oldTelemetry := testTelemetry
	oldRooms := testMatchmaker.Rooms
	oldSessions := testRegistry.Sessions
	defer func() {
		testPresence.Online = oldOnline
		testTelemetry = oldTelemetry
		testMatchmaker.Rooms = oldRooms
		testRegistry.Sessions = oldSessions
	}()
	testPresence.Online = map[uint64]*platform.PlayerID{1: {PID: 101, Username: "host", Title: 5760}, 2: {PID: 102, Username: "joined", Title: 5760}, 3: {PID: 103, Username: "pending", Title: 5760}, 4: {PID: 104, Username: "diablo"}}
	testTelemetry = dashboard.NewTelemetry(testConfig, func() int64 { return 0 })
	for id := range testPresence.Online {
		testTelemetry.RegisterConn(id, "")
	}
	host := &ctr.Search{PID: 101, Doc: ctr.SearchDoc{Members: []uint64{101}}}
	guest := &ctr.Search{PID: 102, Doc: ctr.SearchDoc{Members: []uint64{102, 103}}}
	testMatchmaker.Rooms = map[uint64]*ctr.Room{88: {ID: 88, Host: host, Players: []*ctr.Search{host, guest}, Capacity: 8, HostDoc: map[string]any{"player_state": map[string]any{"101": json.Number("2"), "102": json.Number("2"), "103": json.Number("1")}}}}
	testRegistry.Sessions = map[[8]byte]*lobby.MatchSession{{1}: {Owner: 1, NumPlayers: 2, MaxPlayers: 8}, {2}: {Owner: 2, NumPlayers: 1, MaxPlayers: 8}}
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
