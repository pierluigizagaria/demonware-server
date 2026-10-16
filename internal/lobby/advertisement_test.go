package lobby

import "testing"

func TestOwnAdvertisementCopiesOnlyLiveOwnerState(t *testing.T) {
	r := NewSessionRegistry()
	id := [8]byte{1}
	r.sessions[id] = &MatchSession{ID: id, Owner: 7, OwnerPID: 101, Context: "ctr:Delta", hostAddr: make([]byte, 67), Attrs: []byte{1, 2}, MaxPlayers: 8, NumPlayers: 1}
	a, ok := r.OwnAdvertisement(7, 101, "ctr:Delta")
	if !ok {
		t.Fatal("live owner missing")
	}
	a.Address[0] = 255
	a.Attributes[0] = 255
	a.SessionID[0] = 255
	b, _ := r.OwnAdvertisement(7, 101, "ctr:Delta")
	if b.Address[0] != 0 || b.Attributes[0] != 1 || b.SessionID[0] != 1 {
		t.Fatal("advertisement exposes mutable state")
	}
	for _, q := range []struct {
		conn, pid uint64
		context   string
	}{{8, 101, "ctr:Delta"}, {7, 102, "ctr:Delta"}, {7, 101, "ctr:other"}} {
		if _, ok := r.OwnAdvertisement(q.conn, q.pid, q.context); ok {
			t.Fatal("foreign or stale record exported")
		}
	}
	r.sessions[id].Reserved = true
	if _, ok := r.OwnAdvertisement(7, 101, "ctr:Delta"); ok {
		t.Fatal("reserved record exported")
	}
	r.sessions[id].Reserved = false
	r.sessions[id].Owner = 0
	if _, ok := r.OwnAdvertisement(7, 101, "ctr:Delta"); ok {
		t.Fatal("orphan record exported")
	}
}
