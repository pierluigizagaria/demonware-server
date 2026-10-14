package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

func fillTestRoom(t *testing.T, m *ctr.Matchmaker, base uint64, count int) *ctr.Room {
	t.Helper()
	a := testCTRSearchFor(m, t, base, 0)
	m.Enqueue(a)
	out := m.Enqueue(testCTRSearchFor(m, t, base+1, 0))
	r := m.Rooms[out[0].IDs[1]]
	for i := 2; i < count; i++ {
		m.Enqueue(testCTRSearchFor(m, t, base+uint64(i), 0))
	}
	states := map[string]int{}
	for _, p := range r.Players {
		states[strconv.FormatUint(p.PID, 10)] = 2
	}
	r.HostDoc["player_state"] = states
	raw, _ := json.Marshal(r.HostDoc)
	if ok, _ := m.SyncDoc(a.Conn, r.ID, r.Version, string(raw)); !ok {
		t.Fatal("sync failed")
	}
	return r
}

func TestCTRFailedReservationsAllowFivePlusThree(t *testing.T) {
	m := ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	r := fillTestRoom(t, &m, 1000, 8)
	states := map[string]int{}
	for i := 0; i < 8; i++ {
		state := 2
		if i >= 5 {
			state = 3
		}
		states[strconv.Itoa(1000+i)] = state
	}
	r.HostDoc["player_state"] = states
	raw, _ := json.Marshal(r.HostDoc)
	if ok, _ := m.SyncDoc(r.Host.Conn, r.ID, r.Version, string(raw)); !ok {
		t.Fatal("sync rejected")
	}
	if r.Occupancy() != 5 {
		t.Fatalf("failed joins consume seats: %d", r.Occupancy())
	}
	party := testCTRSearchFor(&m, t, 2000, 0)
	party.Doc.Members = []uint64{2000, 2001, 2002}
	party.Seats = 3
	out := m.Enqueue(party)
	if len(out) != 1 || out[0].Event != ctr.EventUpdateLobby || out[0].IDs[0] != r.ID || len(m.Rooms) != 1 {
		t.Fatal("party did not backfill five-player room")
	}
	h, b := r.Documents()
	var backend struct {
		Players map[string]string `json:"players"`
	}
	json.Unmarshal([]byte(b), &backend)
	if len(backend.Players) != 8 || backend.Players["1005"] != "" || backend.Players["2002"] != "expected" {
		t.Fatal("incorrect expected membership")
	}
	ok, notices := m.SyncDoc(r.Host.Conn, r.ID, r.Version, h)
	if !ok || len(notices) != 1 || notices[0].Conn != party.Conn || notices[0].Event != ctr.EventJoinLobby {
		t.Fatal("party not released after host sync")
	}
	if ok, notices := m.SyncDoc(r.Host.Conn, r.ID, r.Version, h); !ok || len(notices) != 0 {
		t.Fatal("sync generated repeated notifications")
	}
}

func TestCTRFullestOpenRoomWins(t *testing.T) {
	m := ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	fuller := fillTestRoom(t, &m, 3000, 5)
	fuller.HostDoc["lobby_open"] = false
	smaller := fillTestRoom(t, &m, 4000, 2)
	fuller.HostDoc["lobby_open"] = true
	out := m.Enqueue(testCTRSearchFor(&m, t, 5000, 0))
	if len(out) != 1 || out[0].IDs[0] != fuller.ID {
		t.Fatal("did not fill fullest room")
	}
	fuller.HostDoc["lobby_open"] = false
	out = m.Enqueue(testCTRSearchFor(&m, t, 5001, 0))
	if len(out) != 1 || out[0].IDs[0] != smaller.ID {
		t.Fatal("selected closed room")
	}
}

func TestCTRRejectDuplicatePartyMembers(t *testing.T) {
	s := testCTRSearch(t, 6000, 0)
	s.Doc.Members = []uint64{6000, 6000}
	raw, _ := json.Marshal(s.Doc)
	if _, err := testMatchmaker.ParseSearch(s.Conn, string(raw), s.Conn.(*lobbyConn).lobbyDoc); err == nil {
		t.Fatal("duplicate members accepted")
	}
	s.Doc.Members = []uint64{6000, 0}
	raw, _ = json.Marshal(s.Doc)
	if _, err := testMatchmaker.ParseSearch(s.Conn, string(raw), s.Conn.(*lobbyConn).lobbyDoc); err == nil {
		t.Fatal("zero member accepted")
	}
}

func TestCTRWaitingSearchBackfilledWhenHostOpens(t *testing.T) {
	m := ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	r := fillTestRoom(t, &m, 7000, 5)
	r.HostDoc["lobby_open"] = false
	guest := testCTRSearchFor(&m, t, 8000, 0)
	if out := m.Enqueue(guest); len(out) != 0 || len(m.Waiting) != 1 {
		t.Fatal("guest should wait while room closed")
	}
	r.HostDoc["lobby_open"] = true
	h, _ := r.Documents()
	ok, out := m.SyncDoc(r.Host.Conn, r.ID, r.Version, h)
	if !ok || len(out) != 1 || out[0].Event != ctr.EventUpdateLobby || len(m.Waiting) != 0 {
		t.Fatal("opening room did not pick up existing waiter")
	}
	h, _ = r.Documents()
	ok, out = m.SyncDoc(r.Host.Conn, r.ID, r.Version, h)
	if !ok || len(out) != 1 || out[0].Event != ctr.EventJoinLobby || out[0].Conn != guest.Conn {
		t.Fatal("waiter not released after host acknowledged membership")
	}
}

func partySearchFor(m *ctr.Matchmaker, t *testing.T, leader uint64, members ...uint64) *ctr.Search {
	t.Helper()
	s := testCTRSearchFor(m, t, leader, 0)
	s.Doc.Members = append([]uint64{leader}, members...)
	s.Seats = len(s.Doc.Members)
	return s
}

// seatsHeld counts, per player, the seats held across waiting searches and rooms.
func seatsHeld(m *ctr.Matchmaker) map[uint64]int {
	seats := map[uint64]int{}
	count := func(p *ctr.Search) {
		for _, pid := range p.Doc.Members {
			seats[pid]++
		}
	}
	for _, r := range m.Rooms {
		for _, p := range r.Players {
			count(p)
		}
	}
	for _, p := range m.Waiting {
		count(p)
	}
	return seats
}

// A party member who searches alone leaves the party: two people never hold
// three seats, whether the party is still waiting or already seated.
func TestCTRPartyMemberSearchingAloneHoldsOneSeat(t *testing.T) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	party := partySearchFor(m, t, 7000, 7001)
	party.Doc.Slots.Min = 8 // keeps waiting
	m.Enqueue(party)
	m.Enqueue(testCTRSearchFor(m, t, 7001, 1)) // another playlist: both stay waiting
	if seats := seatsHeld(m); len(m.Waiting) != 2 || seats[7000] != 1 || seats[7001] != 1 {
		t.Fatalf("seats %v after a waiting party member searched alone", seats)
	}

	m = &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	r := fillTestRoom(t, m, 7100, 2)
	m.Enqueue(partySearchFor(m, t, 7110, 7111))
	if r.Occupancy() != 4 {
		t.Fatalf("party not seated: %d", r.Occupancy())
	}
	version := r.Version
	out := m.Enqueue(testCTRSearchFor(m, t, 7111, 0))
	if seats := seatsHeld(m); seats[7111] != 1 || seats[7110] != 1 || r.Version <= version || len(out) == 0 || out[0].Conn != r.Host.Conn {
		t.Fatalf("seats %v after a seated party member searched alone", seats)
	}
}

// Party members are only the leader's claim: a claim can never take a seat
// another player holds, whether that player waits or hosts a room.
func TestCTRPartyClaimCannotEvictAnotherPlayer(t *testing.T) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	m.Enqueue(testCTRSearchFor(m, t, 7201, 1))
	if err := m.Reserve(partySearchFor(m, t, 7200, 7201)); err == nil {
		t.Fatal("claim on a waiting player accepted")
	}
	if seats := seatsHeld(m); seats[7201] != 1 || len(m.Waiting) != 1 || m.Waiting[0].PID != 7201 || len(m.Pending) != 0 {
		t.Fatalf("waiting player's search changed: %v", seats)
	}
	r := fillTestRoom(t, m, 7300, 2)
	if err := m.Reserve(partySearchFor(m, t, 7310, r.Host.PID)); err == nil {
		t.Fatal("claim on a room host accepted")
	}
	if m.Rooms[r.ID] != r || r.Occupancy() != 2 {
		t.Fatal("claimed host's room changed")
	}
}

// The conflict check and the reservation are one step, so two parties are
// never both told they claimed the same free player.
func TestCTRConcurrentPartyClaimsReserveAtomically(t *testing.T) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	first, second := partySearchFor(m, t, 7400, 7402), partySearchFor(m, t, 7401, 7402)
	first.Doc.Slots.Min, second.Doc.Slots.Min = 8, 8
	if err := m.Reserve(first); err != nil {
		t.Fatal(err)
	}
	if err := m.Reserve(second); err == nil {
		t.Fatal("second claim on a reserved player accepted")
	}
	m.Release(first) // its reply could not be sent
	if err := m.Reserve(second); err != nil {
		t.Fatal("released reservation still blocks:", err)
	}
	m.Activate(second)
	if seats := seatsHeld(m); seats[7402] != 1 || len(m.Pending) != 0 {
		t.Fatalf("seats %v after activation", seats)
	}
}

// A follower who searches alone while the leader's reply is in flight leaves
// the reserved party; the leader's acknowledged search is still activated,
// and a failed reply still releases the reduced reservation.
func TestCTRReservedPartyShrinksBeforeActivation(t *testing.T) {
	for _, replySent := range []bool{true, false} {
		m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
		leader := partySearchFor(m, t, 7450, 7451)
		leader.Doc.Slots.Min = 8
		if err := m.Reserve(leader); err != nil {
			t.Fatal(err)
		}
		m.Enqueue(testCTRSearchFor(m, t, 7451, 1))
		if replySent {
			m.Activate(leader)
			if len(m.Waiting) != 2 || len(m.Pending) != 0 || !slices.ContainsFunc(m.Waiting, func(p *ctr.Search) bool { return p.ID == leader.ID && p.Seats == 1 }) {
				t.Fatal("acknowledged leader search dropped or kept the departed follower")
			}
		} else {
			m.Release(leader)
			if len(m.Pending) != 0 || len(m.Waiting) != 1 {
				t.Fatal("reduced reservation not released")
			}
		}
	}
}

// A connected follower who disbands its lobby gives its seat back.
func TestCTRFollowerDisbandReleasesSeat(t *testing.T) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	r := fillTestRoom(t, m, 7800, 2)
	m.Enqueue(partySearchFor(m, t, 7810, 7811))
	follower := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 7811, Player: &platform.PlayerID{PID: 7811}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	testPresence.OnlineMu.Lock()
	testPresence.Online[follower.N] = follower.Player
	testPresence.OnlineMu.Unlock()
	defer testPresence.DropOnline(follower.N)
	version := r.Version
	out := m.Leave(follower, r.ID)
	if seats := seatsHeld(m); seats[7811] != 0 || r.Version != version+1 || len(out) != 1 || out[0].Conn != r.Host.Conn {
		t.Fatalf("seats %v after a connected follower disbanded", seats)
	}
}

// A leader who searches again with the same party replaces its own search.
func TestCTRPartyRetryReplacesOwnSearch(t *testing.T) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	for range 2 {
		s := partySearchFor(m, t, 7500, 7501)
		s.Doc.Slots.Min = 8
		if err := m.Reserve(s); err != nil {
			t.Fatal("retry refused:", err)
		}
		m.Enqueue(s)
	}
	if seats := seatsHeld(m); len(m.Waiting) != 1 || seats[7500] != 1 || seats[7501] != 1 {
		t.Fatalf("seats %v after a retry", seats)
	}
}

// A late disconnect of an account's old connection leaves the seats its
// newer connection holds.
func TestCTRStaleDisconnectKeepsReplacementSeats(t *testing.T) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	party := partySearchFor(m, t, 7600, 7601)
	party.Doc.Slots.Min = 8
	m.Enqueue(party)
	old := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 7699, Player: &platform.PlayerID{PID: 7601}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	testPresence.OnlineMu.Lock()
	testPresence.Online[7698] = &platform.PlayerID{PID: 7601} // the replacement connection
	testPresence.OnlineMu.Unlock()
	defer testPresence.DropOnline(7698)
	m.Leave(old, 0)
	if seats := seatsHeld(m); seats[7601] != 1 {
		t.Fatalf("stale disconnect removed the live seat: %v", seats)
	}
}

// Disbanding an old lobby ends no search: a player who already searches again
// keeps that search.
func TestCTROldLobbyDisbandKeepsNewSearch(t *testing.T) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	r := fillTestRoom(t, m, 7700, 3)
	player := r.Players[2]
	again := testCTRSearchFor(m, t, player.PID, 1)
	again.Conn = player.Conn
	m.Enqueue(again)
	m.Leave(player.Conn, r.ID)
	if len(m.Waiting) != 1 || m.Waiting[0] != again {
		t.Fatal("old lobby disband cancelled the new search")
	}
}
