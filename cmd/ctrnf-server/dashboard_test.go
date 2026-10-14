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
	oldMatchmaker := testMatchmaker
	oldRegistry := testRegistry
	defer func() {
		testPresence = oldPresence
		testTelemetry = oldTelemetry
		testMatchmaker = oldMatchmaker
		testRegistry = oldRegistry
	}()
	players := map[uint64]*platform.PlayerID{1: {PID: 101, Username: "host", Title: 5760}, 2: {PID: 102, Username: "joined", Title: 5760}, 3: {PID: 103, Username: "pending", Title: 5760}, 4: {PID: 104, Username: "diablo"}}
	testTelemetry = dashboard.NewTelemetry(testConfig, func() int64 { return 0 })
	testPresence = presence.NewStore(nil)
	for id, player := range players {
		testPresence.RegisterOnline(id, player)
		testTelemetry.RegisterConn(id, "")
	}
	testMatchmaker = ctr.NewMatchmaker(testPresence.OnlinePIDs)
	host := testCTRSearchFor(testMatchmaker, t, 101, 0)
	guest := testCTRSearchFor(testMatchmaker, t, 102, 0)
	guest.Doc.Members, guest.Seats = []uint64{102, 103}, 2
	testMatchmaker.Enqueue(host)
	notices := testMatchmaker.Enqueue(guest)
	if len(notices) != 1 {
		t.Fatal("dashboard room was not formed")
	}
	roomID := notices[0].IDs[1]
	raw, _, ok := testMatchmaker.Documents(host.Conn, roomID)
	if !ok {
		t.Fatal("missing room document")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	doc["player_state"] = map[string]uint32{"101": 2, "102": 2, "103": 1}
	rawDoc, _ := json.Marshal(doc)
	if ok, _ := testMatchmaker.SyncDoc(host.Conn, roomID, 1, string(rawDoc)); !ok {
		t.Fatal("host document refused")
	}
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
		if p.PID == 102 && (p.IsHost || p.Gathering != uint32(roomID)) {
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
