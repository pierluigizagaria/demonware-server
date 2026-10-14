package ctr

import "testing"

func TestMatchmakingObservationDoesNotExposeQueue(t *testing.T) {
	m := NewMatchmaker(nil)
	m.waiting = []*Search{{ID: 7}}
	m.pending = map[uint64]*Search{8: {ID: 8}}
	m.rooms[9] = &Room{ID: 9}
	view := m.Snapshot()
	if len(view.WaitingIDs) != 1 || view.WaitingIDs[0] != 7 || view.Pending != 1 || view.Rooms != 1 {
		t.Fatal("incomplete matchmaking observation")
	}
	view.WaitingIDs[0] = 99
	if m.Snapshot().WaitingIDs[0] != 7 {
		t.Fatal("observation mutated the live queue")
	}
}
