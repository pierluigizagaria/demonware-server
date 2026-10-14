package main

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
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
