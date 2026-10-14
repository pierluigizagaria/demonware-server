package ctr

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

func psnTicket(issued time.Time) []byte {
	ticket := make([]byte, 128)
	binary.LittleEndian.PutUint32(ticket, auth.TicketMagic)
	ticket[4] = 1
	binary.LittleEndian.PutUint32(ticket[5:], 5760)
	binary.LittleEndian.PutUint32(ticket[13:], uint32(issued.Unix()))
	binary.LittleEndian.PutUint64(ticket[17:], 17)
	binary.LittleEndian.PutUint64(ticket[25:], 17)
	_, _ = rand.Read(ticket[33:97])
	copy(ticket[97:121], ticket[33:57])
	return ticket
}

func TestPS4UmbrellaUsesFreshIssuedTicket(t *testing.T) {
	tickets := auth.NewTicketStore()
	testUmbrellaHandler := NewUmbrellaHandler(&diagnostics.Diagnostics{RedactLogs: true}, tickets)
	issueTestTicket := func(ticket []byte, id platform.PlayerID) {
		tickets.RememberTicket(ticket, id, time.Now(), ps4.TicketWindow)
	}

	call := func(ticket []byte) int {
		body, _ := json.Marshal(map[string]any{"ticket": ticket, "titleID": "5760"})
		w := httptest.NewRecorder()
		testUmbrellaHandler(w, httptest.NewRequest("POST", "/v1.0/tokens/lsg/", bytes.NewReader(body)), body, 1, "")
		return w.Code
	}
	fresh, stale := psnTicket(time.Now()), psnTicket(time.Now().Add(-121*time.Second))
	for _, ticket := range [][]byte{fresh, stale} {
		issueTestTicket(ticket, platform.PlayerID{PID: 17, Username: "fixture"})
	}
	if call(fresh) != 200 || call(stale) != 401 || call(psnTicket(time.Now())) != 401 {
		t.Fatal("Umbrella did not apply fresh issued-ticket admission")
	}
}

func TestCTRFollowerLeaveKeepsPartyAndNotifiesHost(t *testing.T) {
	testPresence := presence.NewStore(nil)
	testCTR := &Services{Economy: &EconomyStore{Dir: t.TempDir()}, Stats: &StatsStore{Dir: t.TempDir()}}

	m := &matchFixture{Matchmaker: Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*Room{}}}
	r := fillTestRoom(t, m, 30000, 2)
	leader := r.Players[1]
	leader.Doc.Members = []uint64{leader.PID, 30009}
	leader.Seats = 2
	follower := newUnitPeer(t, &testPeer{Presence: testPresence, N: 30009, Player: &platform.PlayerID{PID: 30009}, services: testCTR})
	version := r.Version
	out := m.Leave(follower, 0)
	if len(out) != 1 || out[0].Conn != r.Host.Conn || out[0].Event != EventUpdateLobby || r.Version != version+1 {
		t.Fatal("host not notified of the follower's departure")
	}
	if got := r.Players[1].Doc.Members; len(got) != 1 || got[0] != leader.PID || r.Occupancy() != 2 {
		t.Fatal("follower seat not released")
	}
	if out := m.Leave(leader.Conn, 0); len(out) != 1 || len(r.Players) != 1 {
		t.Fatal("leader departure did not remove its search")
	}
}
