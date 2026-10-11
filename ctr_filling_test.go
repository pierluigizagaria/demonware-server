package main

import (
	"encoding/json"
	"strconv"
	"testing"
)

func fillTestRoom(t *testing.T, m *ctrMatchmaker, base uint64, count int) *ctrRoom {
	t.Helper()
	a := testCTRSearch(t, base, 0)
	m.enqueue(a)
	out := m.enqueue(testCTRSearch(t, base+1, 0))
	r := m.rooms[out[0].ids[1]]
	for i := 2; i < count; i++ {
		m.enqueue(testCTRSearch(t, base+uint64(i), 0))
	}
	states := map[string]int{}
	for _, p := range r.players {
		states[strconv.FormatUint(p.pid, 10)] = 2
	}
	r.hostDoc["player_state"] = states
	raw, _ := json.Marshal(r.hostDoc)
	if ok, _ := m.syncDoc(a.conn, r.id, r.version, string(raw)); !ok {
		t.Fatal("sync failed")
	}
	return r
}

func TestCTRFailedReservationsAllowFivePlusThree(t *testing.T) {
	m := ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	r := fillTestRoom(t, &m, 1000, 8)
	states := map[string]int{}
	for i := 0; i < 8; i++ {
		state := 2
		if i >= 5 {
			state = 3
		}
		states[strconv.Itoa(1000+i)] = state
	}
	r.hostDoc["player_state"] = states
	raw, _ := json.Marshal(r.hostDoc)
	if ok, _ := m.syncDoc(r.host.conn, r.id, r.version, string(raw)); !ok {
		t.Fatal("sync rejected")
	}
	if r.occupancy() != 5 {
		t.Fatalf("failed joins consume seats: %d", r.occupancy())
	}
	party := testCTRSearch(t, 2000, 0)
	party.doc.Members = []uint64{2000, 2001, 2002}
	party.seats = 3
	out := m.enqueue(party)
	if len(out) != 1 || out[0].event != ctrUpdateLobby || out[0].ids[0] != r.id || len(m.rooms) != 1 {
		t.Fatal("party did not backfill five-player room")
	}
	h, b := r.documents()
	var backend struct {
		Players map[string]string `json:"players"`
	}
	json.Unmarshal([]byte(b), &backend)
	if len(backend.Players) != 8 || backend.Players["1005"] != "" || backend.Players["2002"] != "expected" {
		t.Fatal("incorrect expected membership")
	}
	ok, notices := m.syncDoc(r.host.conn, r.id, r.version, h)
	if !ok || len(notices) != 1 || notices[0].conn != party.conn || notices[0].event != ctrJoinLobby {
		t.Fatal("party not released after host sync")
	}
	if ok, notices := m.syncDoc(r.host.conn, r.id, r.version, h); !ok || len(notices) != 0 {
		t.Fatal("sync generated repeated notifications")
	}
}

func TestCTRFullestOpenRoomWins(t *testing.T) {
	m := ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	fuller := fillTestRoom(t, &m, 3000, 5)
	fuller.hostDoc["lobby_open"] = false
	smaller := fillTestRoom(t, &m, 4000, 2)
	fuller.hostDoc["lobby_open"] = true
	out := m.enqueue(testCTRSearch(t, 5000, 0))
	if len(out) != 1 || out[0].ids[0] != fuller.id {
		t.Fatal("did not fill fullest room")
	}
	fuller.hostDoc["lobby_open"] = false
	out = m.enqueue(testCTRSearch(t, 5001, 0))
	if len(out) != 1 || out[0].ids[0] != smaller.id {
		t.Fatal("selected closed room")
	}
}

func TestCTRRejectDuplicatePartyMembers(t *testing.T) {
	s := testCTRSearch(t, 6000, 0)
	s.doc.Members = []uint64{6000, 6000}
	raw, _ := json.Marshal(s.doc)
	if _, err := parseCTRSearch(s.conn, string(raw)); err == nil {
		t.Fatal("duplicate members accepted")
	}
	s.doc.Members = []uint64{6000, 0}
	raw, _ = json.Marshal(s.doc)
	if _, err := parseCTRSearch(s.conn, string(raw)); err == nil {
		t.Fatal("zero member accepted")
	}
}

func TestCTRWaitingSearchBackfilledWhenHostOpens(t *testing.T) {
	m := ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	r := fillTestRoom(t, &m, 7000, 5)
	r.hostDoc["lobby_open"] = false
	guest := testCTRSearch(t, 8000, 0)
	if out := m.enqueue(guest); len(out) != 0 || len(m.waiting) != 1 {
		t.Fatal("guest should wait while room closed")
	}
	r.hostDoc["lobby_open"] = true
	h, _ := r.documents()
	ok, out := m.syncDoc(r.host.conn, r.id, r.version, h)
	if !ok || len(out) != 1 || out[0].event != ctrUpdateLobby || len(m.waiting) != 0 {
		t.Fatal("opening room did not pick up existing waiter")
	}
	h, _ = r.documents()
	ok, out = m.syncDoc(r.host.conn, r.id, r.version, h)
	if !ok || len(out) != 1 || out[0].event != ctrJoinLobby || out[0].conn != guest.conn {
		t.Fatal("waiter not released after host acknowledged membership")
	}
}

func partySearch(t *testing.T, leader uint64, members ...uint64) *ctrSearch {
	t.Helper()
	s := testCTRSearch(t, leader, 0)
	s.doc.Members = append([]uint64{leader}, members...)
	s.seats = len(s.doc.Members)
	return s
}

// seatsHeld counts, per player, the seats held across waiting searches and rooms.
func seatsHeld(m *ctrMatchmaker) map[uint64]int {
	seats := map[uint64]int{}
	count := func(p *ctrSearch) {
		for _, pid := range p.doc.Members {
			seats[pid]++
		}
	}
	for _, r := range m.rooms {
		for _, p := range r.players {
			count(p)
		}
	}
	for _, p := range m.waiting {
		count(p)
	}
	return seats
}

// A party member who searches alone leaves the party: two people never hold
// three seats, whether the party is still waiting or already seated.
func TestCTRPartyMemberSearchingAloneHoldsOneSeat(t *testing.T) {
	m := &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	party := partySearch(t, 7000, 7001)
	party.doc.Slots.Min = 8 // keeps waiting
	m.enqueue(party)
	m.enqueue(testCTRSearch(t, 7001, 1)) // another playlist: both stay waiting
	if seats := seatsHeld(m); len(m.waiting) != 2 || seats[7000] != 1 || seats[7001] != 1 {
		t.Fatalf("seats %v after a waiting party member searched alone", seats)
	}

	m = &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	r := fillTestRoom(t, m, 7100, 2)
	m.enqueue(partySearch(t, 7110, 7111))
	if r.occupancy() != 4 {
		t.Fatalf("party not seated: %d", r.occupancy())
	}
	version := r.version
	out := m.enqueue(testCTRSearch(t, 7111, 0))
	if seats := seatsHeld(m); seats[7111] != 1 || seats[7110] != 1 || r.version <= version || len(out) == 0 || out[0].conn != r.host.conn {
		t.Fatalf("seats %v after a seated party member searched alone", seats)
	}
}

// Party members are only the leader's claim: a claim can never take a seat
// another player holds, whether that player waits or hosts a room.
func TestCTRPartyClaimCannotEvictAnotherPlayer(t *testing.T) {
	m := &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	m.enqueue(testCTRSearch(t, 7201, 1))
	if err := m.reserve(partySearch(t, 7200, 7201)); err == nil {
		t.Fatal("claim on a waiting player accepted")
	}
	if seats := seatsHeld(m); seats[7201] != 1 || len(m.waiting) != 1 || m.waiting[0].pid != 7201 || len(m.pending) != 0 {
		t.Fatalf("waiting player's search changed: %v", seats)
	}
	r := fillTestRoom(t, m, 7300, 2)
	if err := m.reserve(partySearch(t, 7310, r.host.pid)); err == nil {
		t.Fatal("claim on a room host accepted")
	}
	if m.rooms[r.id] != r || r.occupancy() != 2 {
		t.Fatal("claimed host's room changed")
	}
}

// The conflict check and the reservation are one step, so two parties are
// never both told they claimed the same free player.
func TestCTRConcurrentPartyClaimsReserveAtomically(t *testing.T) {
	m := &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	first, second := partySearch(t, 7400, 7402), partySearch(t, 7401, 7402)
	first.doc.Slots.Min, second.doc.Slots.Min = 8, 8
	if err := m.reserve(first); err != nil {
		t.Fatal(err)
	}
	if err := m.reserve(second); err == nil {
		t.Fatal("second claim on a reserved player accepted")
	}
	m.release(first) // its reply could not be sent
	if err := m.reserve(second); err != nil {
		t.Fatal("released reservation still blocks:", err)
	}
	m.enqueue(second)
	if seats := seatsHeld(m); seats[7402] != 1 || len(m.pending) != 0 {
		t.Fatalf("seats %v after activation", seats)
	}
}

// A leader who searches again with the same party replaces its own search.
func TestCTRPartyRetryReplacesOwnSearch(t *testing.T) {
	m := &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	for range 2 {
		s := partySearch(t, 7500, 7501)
		s.doc.Slots.Min = 8
		if err := m.reserve(s); err != nil {
			t.Fatal("retry refused:", err)
		}
		m.enqueue(s)
	}
	if seats := seatsHeld(m); len(m.waiting) != 1 || seats[7500] != 1 || seats[7501] != 1 {
		t.Fatalf("seats %v after a retry", seats)
	}
}

// A late disconnect of an account's old connection leaves the seats its
// newer connection holds.
func TestCTRStaleDisconnectKeepsReplacementSeats(t *testing.T) {
	m := &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	party := partySearch(t, 7600, 7601)
	party.doc.Slots.Min = 8
	m.enqueue(party)
	old := &lobbyConn{n: 7699, player: &playerID{PID: 7601}}
	onlineMu.Lock()
	online[7698] = &playerID{PID: 7601} // the replacement connection
	onlineMu.Unlock()
	defer dropOnline(7698)
	m.leave(old, 0)
	if seats := seatsHeld(m); seats[7601] != 1 {
		t.Fatalf("stale disconnect removed the live seat: %v", seats)
	}
}

// Disbanding an old lobby ends no search: a player who already searches again
// keeps that search.
func TestCTROldLobbyDisbandKeepsNewSearch(t *testing.T) {
	m := &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	r := fillTestRoom(t, m, 7700, 3)
	player := r.players[2]
	again := testCTRSearch(t, player.pid, 1)
	again.conn = player.conn
	m.enqueue(again)
	m.leave(player.conn, r.id)
	if len(m.waiting) != 1 || m.waiting[0] != again {
		t.Fatal("old lobby disband cancelled the new search")
	}
}
