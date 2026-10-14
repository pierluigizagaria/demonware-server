package main

import (
	json "encoding/json"
	ctrpkg "github.com/NextendoNetwork/nextendo-demonware/internal/ctr"
	dashboardpkg "github.com/NextendoNetwork/nextendo-demonware/internal/dashboard"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	testing "testing"
	time "time"
)

func TestDashboardCTRMembershipAndTitleSplit(t *testing.T) {
	oldOnline := testPresence.Online
	oldStats := testTelemetry.ConnStats
	oldRooms := testMatchmaker.Rooms
	oldSessions := testRegistry.Sessions
	defer func() {
		testPresence.Online = oldOnline
		testTelemetry.ConnStats = oldStats
		testMatchmaker.Rooms = oldRooms
		testRegistry.Sessions = oldSessions
	}()
	testPresence.Online = map[uint64]*platformpkg.PlayerID{1: {PID: 101, Username: "host", Title: 5760}, 2: {PID: 102, Username: "joined", Title: 5760}, 3: {PID: 103, Username: "pending", Title: 5760}, 4: {PID: 104, Username: "diablo"}}
	testTelemetry.ConnStats = map[uint64]*dashboardpkg.ConnStat{}
	for id := range testPresence.Online {
		testTelemetry.ConnStats[id] = &dashboardpkg.ConnStat{First: time.Now(), Last: time.Now()}
	}
	host := &ctrpkg.CtrSearch{Pid: 101, Doc: ctrpkg.CtrSearchDoc{Members: []uint64{101}}}
	guest := &ctrpkg.CtrSearch{Pid: 102, Doc: ctrpkg.CtrSearchDoc{Members: []uint64{102, 103}}}
	testMatchmaker.Rooms = map[uint64]*ctrpkg.CtrRoom{88: {Id: 88, Host: host, Players: []*ctrpkg.CtrSearch{host, guest}, Capacity: 8, HostDoc: map[string]any{"player_state": map[string]any{"101": json.Number("2"), "102": json.Number("2"), "103": json.Number("1")}}}}
	testRegistry.Sessions = map[[8]byte]*lobbypkg.MmSession{{1}: {Owner: 1, NumPlayers: 2, MaxPlayers: 8}, {2}: {Owner: 2, NumPlayers: 1, MaxPlayers: 8}}
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
