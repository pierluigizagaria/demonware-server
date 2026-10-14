package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

func testCTRSearchFor(m *ctr.CtrMatchmaker, t *testing.T, pid uint64, playlist int) *ctr.CtrSearch {
	t.Helper()
	info := ctr.CtrPlayerInfo{}
	info.Listen.Address = base64.StdEncoding.EncodeToString(make([]byte, 67))
	info.Listen.ID = base64.StdEncoding.EncodeToString(make([]byte, 8))
	info.Listen.Key = base64.StdEncoding.EncodeToString(make([]byte, 16))
	raw, _ := json.Marshal(info)
	l := newTestLobby(&lobby.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: testSearchConns.Add(1), Player: &platform.PlayerID{PID: pid}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: m, registry: testRegistry, ctr: testCTR, lobbyDoc: string(raw)})
	search, err := m.ParseCTRSearch(l, fmt.Sprintf(`{"members":[%d],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":%d,"matchmaking_version":200}}}`, pid, playlist), l.lobbyDoc)
	if err != nil {
		t.Fatal(err)
	}
	return search
}

func TestCTRPublicPairingAndDocuments(t *testing.T) {
	m := ctr.CtrMatchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.CtrRoom{}}
	a, b, c := testCTRSearchFor(&m, t, 101, 0), testCTRSearchFor(&m, t, 102, 0), testCTRSearchFor(&m, t, 103, 1)
	if len(m.Enqueue(a)) != 0 || len(m.Enqueue(c)) != 0 {
		t.Fatal("matched incompatible searches")
	}
	out := m.Enqueue(b)
	if len(out) != 1 || out[0].Event != ctr.CtrCreateLobby || out[0].Conn != a.Conn {
		t.Fatal("host must create lobby before guests join")
	}
	id := out[0].Ids[1]
	ready, notifications := m.SyncDoc(a.Conn, id, 1, out[0].Host)
	if !ready || len(notifications) != 1 || notifications[0].Event != ctr.CtrJoinLobby || notifications[0].Conn != b.Conn {
		t.Fatal("ready host did not release guest join")
	}
	out = append(out, notifications[0])

	if len(m.Waiting) != 1 || m.Waiting[0] != c {
		t.Fatal("lost incompatible search")
	}
	for _, n := range out {
		w := &demonware.Writer{}
		n.Write(w)
		r := demonware.NewReader(w.Bytes())
		wantIDs := 2
		if n.Event == 2005 {
			wantIDs = 3
		}
		for i := 0; i < wantIDs; i++ {
			if id, err := r.Uint64(); err != nil || id == 0 {
				t.Fatal("invalid notification ID", err)
			}
		}
		h, e1 := r.String()
		b, e2 := r.String()
		if e1 != nil || e2 != nil || len(r.Remaining()) != 0 {
			t.Fatal("notification wire layout")
		}
		var host, backend map[string]json.RawMessage
		if json.Unmarshal([]byte(h), &host) != nil || json.Unmarshal([]byte(b), &backend) != nil {
			t.Fatal("invalid documents")
		}
		for _, k := range []string{"update_id", "update_time", "lobby_open", "lobby_open_for_pres_join", "player_state", "listen_server", "team_balance", "ruleset_payload", "attachment"} {
			if _, ok := host[k]; !ok {
				t.Fatal("missing host field", k)
			}
		}
		for _, k := range []string{"update_id", "update_time", "lobby_id", "create_time", "players", "dtls", "ruleset_payload"} {
			if _, ok := backend[k]; !ok {
				t.Fatal("missing backend field", k)
			}
		}
		var players map[string]string
		json.Unmarshal(backend["players"], &players)
		if len(players) != 2 || players["101"] != "expected" || players["102"] != "expected" {
			t.Fatal("incorrect expected players")
		}
	}

	h, _, ok := m.Documents(a.Conn, id)
	if !ok {
		t.Fatal("host cannot get room")
	}
	if ok, _ := m.SyncDoc(b.Conn, id, 1, h); ok {
		t.Fatal("guest modified host document")
	}
	// The host re-syncs at the SAME version: the version tracks membership and
	// nobody has joined since. This is what the real client does -- it echoes the
	// update_id of the document it composed, so requiring a higher number here
	// refused every host sync from the second one onwards, and with it the host's
	// ability to close its own lobby.
	if ok, updates := m.SyncDoc(a.Conn, id, 1, h); !ok || len(updates) != 0 {
		t.Fatal("host sync failed")
	}
	if ok, _ := m.SyncDoc(a.Conn, id, 99, h); ok {
		t.Fatal("wrong version accepted")
	}
	m.Leave(a.Conn, 0)
	if len(m.Rooms) != 0 {
		t.Fatal("disconnected host room retained")
	}
	if !m.Cancel(c.Conn, c.Id) || len(m.Waiting) != 0 {
		t.Fatal("search cancellation failed")
	}
}

func TestCTRSearchRetryCannotMatchItself(t *testing.T) {
	m := ctr.CtrMatchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.CtrRoom{}}
	a := testCTRSearchFor(&m, t, 201, 0)
	m.Enqueue(a)
	retry := testCTRSearchFor(&m, t, 201, 0)
	if len(m.Enqueue(retry)) != 0 || len(m.Waiting) != 1 {
		t.Fatal("duplicate account matched")
	}
	m.Leave(retry.Conn, 0)
	if len(m.Waiting) != 0 {
		t.Fatal("disconnected search retained")
	}
}

func testCTRSearch(t *testing.T, pid uint64, playlist int) *ctr.CtrSearch {
	return testCTRSearchFor(testMatchmaker, t, pid, playlist)
}

var testSearchConns atomic.Uint64

// A view of the same connection is a distinct Go object with the same ConnID.
type peerView struct{ ctr.CtrPeer }

func TestCTRPeerOwnershipUsesConnectionID(t *testing.T) {
	m := &ctr.CtrMatchmaker{OnlinePIDs: func() []uint64 { return []uint64{17, 18} }, Rooms: map[uint64]*ctr.CtrRoom{}}
	a, b := testCTRSearchFor(m, t, 17, 0), testCTRSearchFor(m, t, 18, 0)
	m.Enqueue(a)
	notices := m.Enqueue(b)
	room := notices[0].Ids[1]
	if _, _, ok := m.Documents(&peerView{a.Conn}, room); !ok {
		t.Fatal("another view of the owner connection was refused")
	}
	replacement := testCTRSearchFor(m, t, 17, 0)
	if _, _, ok := m.Documents(replacement.Conn, room); ok {
		t.Fatal("same PID on another connection became the owner")
	}
	m.Leave(replacement.Conn, 0)
	if len(m.Rooms) != 1 {
		t.Fatal("different connection released the room")
	}
	m.Leave(&peerView{a.Conn}, 0)
	if len(m.Rooms) != 0 {
		t.Fatal("owner view did not release the room")
	}
}
