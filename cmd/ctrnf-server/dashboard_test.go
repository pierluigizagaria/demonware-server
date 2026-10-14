package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
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
	testPresence.Online = map[uint64]*platform.PlayerID{1: {PID: 101, Username: "host", Title: 5760}, 2: {PID: 102, Username: "joined", Title: 5760}, 3: {PID: 103, Username: "pending", Title: 5760}, 4: {PID: 104, Username: "diablo"}}
	testTelemetry.ConnStats = map[uint64]*dashboard.ConnStat{}
	for id := range testPresence.Online {
		testTelemetry.ConnStats[id] = &dashboard.ConnStat{First: time.Now(), Last: time.Now()}
	}
	host := &ctr.CtrSearch{Pid: 101, Doc: ctr.CtrSearchDoc{Members: []uint64{101}}}
	guest := &ctr.CtrSearch{Pid: 102, Doc: ctr.CtrSearchDoc{Members: []uint64{102, 103}}}
	testMatchmaker.Rooms = map[uint64]*ctr.CtrRoom{88: {Id: 88, Host: host, Players: []*ctr.CtrSearch{host, guest}, Capacity: 8, HostDoc: map[string]any{"player_state": map[string]any{"101": json.Number("2"), "102": json.Number("2"), "103": json.Number("1")}}}}
	testRegistry.Sessions = map[[8]byte]*lobby.MmSession{{1}: {Owner: 1, NumPlayers: 2, MaxPlayers: 8}, {2}: {Owner: 2, NumPlayers: 1, MaxPlayers: 8}}
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
