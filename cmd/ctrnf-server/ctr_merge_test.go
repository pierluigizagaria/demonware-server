package main

import (
	json "encoding/json"
	ctrpkg "github.com/pierluigizagaria/demonware-server/internal/ctr"
	demonwarepkg "github.com/pierluigizagaria/demonware-server/internal/demonware"
	testing "testing"
)

func mergeTestRooms(t *testing.T) (*ctrpkg.CtrMatchmaker, *ctrpkg.CtrRoom, *ctrpkg.CtrRoom) {
	m := &ctrpkg.CtrMatchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctrpkg.CtrRoom{}}
	a := fillTestRoom(t, m, 10000, 3)
	a.HostDoc["lobby_open"] = false
	b := fillTestRoom(t, m, 20000, 3)
	a.HostDoc["lobby_open"] = true
	return m, a, b
}

func TestCTRMergeThreePlusThreeAfterDestinationAck(t *testing.T) {
	m, a, b := mergeTestRooms(t)
	h, _ := a.Documents()
	ok, out := m.SyncDoc(a.Host.Conn, a.Id, a.Version, h)
	if !ok || len(out) != 1 || out[0].Event != ctrpkg.CtrUpdateLobby {
		t.Fatal("merge did not reserve destination first")
	}
	if len(m.Rooms) != 2 || a.Occupancy() != 6 || len(a.Players) != 3 || b.MergeTarget != a.Id {
		t.Fatal("invalid reserved merge state")
	}
	w := &demonwarepkg.Writer{}
	out[0].Write(w)
	var backend struct {
		Players map[string]string `json:"players"`
	}
	json.Unmarshal([]byte(out[0].Backend), &backend)
	if len(backend.Players) != 6 {
		t.Fatal("destination not told about all incoming members")
	}
	if ok, _ := m.SyncDoc(a.Host.Conn, a.Id, a.Version-1, h); ok {
		t.Fatal("stale host sync accepted")
	}
	h, _ = a.Documents()
	ok, out = m.SyncDoc(a.Host.Conn, a.Id, a.Version, h)
	if !ok || len(out) != 3 || len(m.Rooms) != 1 || a.Occupancy() != 6 {
		t.Fatal("merge not committed")
	}
	for _, n := range out {
		if n.Event != ctrpkg.CtrMergeLobby || len(n.Ids) != 3 || n.Ids[1] != b.Id || n.Ids[2] != a.Id {
			t.Fatal("invalid merge event routing")
		}
		w := &demonwarepkg.Writer{}
		n.Write(w)
		rd := demonwarepkg.NewReader(w.Bytes())
		for _, want := range n.Ids {
			if got, err := rd.Uint64(); err != nil || got != want {
				t.Fatal("merge wire IDs")
			}
		}
		if _, err := rd.String(); err != nil {
			t.Fatal(err)
		}
		if _, err := rd.String(); err != nil {
			t.Fatal(err)
		}
		if len(rd.Remaining()) != 0 {
			t.Fatal("trailing wire bytes")
		}
	}
	if ok, out := m.SyncDoc(a.Host.Conn, a.Id, a.Version, h); !ok || len(out) != 0 {
		t.Fatal("duplicate merge dispatch")
	}
	// Clients may acknowledge disbanding their OLD room after the handoff.
	m.Leave(b.Host.Conn, b.Id)
	if len(a.Players) != 6 {
		t.Fatal("old-room teardown removed destination membership")
	}
}

func TestCTRMergeCancelsIfSourceClosesOrLeaves(t *testing.T) {
	for _, closed := range []bool{true, false} {
		m, a, b := mergeTestRooms(t)
		if len(m.StartMerge(a)) != 1 {
			t.Fatal("no reservation")
		}
		if closed {
			b.HostDoc["lobby_open"] = false
		} else {
			// The source host's departure releases the reservation at once.
			out := m.Leave(b.Host.Conn, b.Id)
			if len(out) != 1 || out[0].Conn != a.Host.Conn || out[0].Event != ctrpkg.CtrUpdateLobby || a.MergeSource != nil || a.Occupancy() != 3 {
				t.Fatal("source departure did not cancel the merge")
			}
			continue
		}
		h, _ := a.Documents()
		ok, out := m.SyncDoc(a.Host.Conn, a.Id, a.Version, h)
		if !ok || len(out) != 1 || out[0].Event != ctrpkg.CtrUpdateLobby || a.MergeSource != nil || a.Occupancy() != 3 {
			t.Fatal("unsafe merge not cancelled")
		}
	}
}

func TestCTRMergeEligibility(t *testing.T) {
	for _, reason := range []string{"closed", "filter", "capacity", "overlap", "unconfirmed"} {
		t.Run(reason, func(t *testing.T) {
			m, a, b := mergeTestRooms(t)
			switch reason {
			case "closed":
				b.HostDoc["lobby_open"] = false
			case "filter":
				b.Host.Filter = "other"
			case "capacity":
				a.Capacity = 5
			case "overlap":
				b.Players[1].Doc.Members = []uint64{a.Host.Pid}
			case "unconfirmed":
				b.HostDoc["player_state"].(map[string]any)["20001"] = json.Number("1")
			}
			if len(m.StartMerge(a)) != 0 {
				t.Fatalf("merged despite %s", reason)
			}
		})
	}
}

func TestCTRMergeKeepsPartyMembersTogether(t *testing.T) {
	m, a, b := mergeTestRooms(t)
	// A three-player party issues one search from its leader.
	p := b.Players[0]
	p.Doc.Members = []uint64{20000, 20001, 20002}
	p.Seats = 3
	b.Players = []*ctrpkg.CtrSearch{p}
	if len(m.StartMerge(a)) != 1 {
		t.Fatal("party merge not reserved")
	}
	h, _ := a.Documents()
	ok, out := m.SyncDoc(a.Host.Conn, a.Id, a.Version, h)
	if !ok || len(out) != 1 || out[0].Conn != p.Conn || a.Occupancy() != 6 {
		t.Fatal("party split or leader not notified")
	}
	var backend struct {
		Players map[string]string `json:"players"`
	}
	json.Unmarshal([]byte(out[0].Backend), &backend)
	for _, pid := range []string{"20000", "20001", "20002"} {
		if backend.Players[pid] != "expected" {
			t.Fatal("missing party member", pid)
		}
	}
}

// A member of the source room who searches again changes the membership the
// destination reserved: the merge is released and both hosts learn it.
func TestCTRRequeueDuringMergeReleasesReservation(t *testing.T) {
	m, a, b := mergeTestRooms(t)
	if len(m.StartMerge(a)) != 1 || a.MergeSource != b {
		t.Fatal("no reservation")
	}
	version := a.Version
	leaver := b.Players[2]
	out := m.Enqueue(testCTRSearchFor(m, t, leaver.Pid, 1))
	if a.MergeSource != nil || b.MergeTarget != 0 || a.Version != version+1 || b.Occupancy() != 2 {
		t.Fatal("reservation kept after the source membership changed")
	}
	hosts := map[ctrpkg.CtrPeer]bool{}
	for _, n := range out {
		if n.Event == ctrpkg.CtrUpdateLobby {
			hosts[n.Conn] = true
		}
	}
	if !hosts[a.Host.Conn] || !hosts[b.Host.Conn] {
		t.Fatal("hosts not told about the changed membership")
	}
}

// Both merge partners closing in one departure release each other without
// a notice to either room.
func TestCTRBothMergePartnersClosing(t *testing.T) {
	m, a, b := mergeTestRooms(t)
	if len(m.StartMerge(a)) != 1 {
		t.Fatal("no reservation")
	}
	b.Host.Conn = a.Host.Conn // one connection hosts both rooms
	if out := m.Leave(a.Host.Conn, 0); len(out) != 0 || len(m.Rooms) != 0 {
		t.Fatalf("closing both partners sent %d notices, %d rooms left", len(out), len(m.Rooms))
	}
	if a.MergeSource != nil || b.MergeTarget != 0 {
		t.Fatal("closed rooms still linked")
	}
}

// A destination host who leaves closes its room; the source is released
// without a notice to the room that is gone.
func TestCTRDestinationHostLeavingReleasesSource(t *testing.T) {
	m, a, b := mergeTestRooms(t)
	if len(m.StartMerge(a)) != 1 {
		t.Fatal("no reservation")
	}
	out := m.Leave(a.Host.Conn, 0)
	for _, n := range out {
		if n.Conn == a.Host.Conn {
			t.Fatal("notice sent for a closed room")
		}
	}
	if m.Rooms[a.Id] != nil || b.MergeTarget != 0 {
		t.Fatal("source still reserved for a closed destination")
	}
}
