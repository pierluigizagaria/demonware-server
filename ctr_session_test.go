package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

func TestCTRFriendBeyondFirstTwenty(t *testing.T) {
	host := &lobbyConn{ctr: testCTR, replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 90343, player: &playerID{PID: 90343}}
	guest := &lobbyConn{ctr: testCTR, replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 99000, player: &playerID{PID: 99000}}
	defer func() {
		sessionsMu.Lock()
		defer sessionsMu.Unlock()
		for id, s := range sessions {
			if s.ownerPID == host.pid() {
				delete(sessions, id)
			}
		}
	}()
	info := &demonware.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	host.onMatchMakingContext(mmCreateSession, demonware.NewReader(info.Bytes()), "ctr:full-list-test")
	for _, size := range []int{20, 80} {
		ids := &demonware.Writer{}
		for i := 1; i <= size; i++ {
			ids.Uint64(uint64(90300 + i))
		}
		reply := guest.onFriendSessions(14, demonware.NewReader(ids.Bytes()), "ctr:full-list-test")
		want := uint32(0)
		if size >= 43 {
			want = 1
		}
		if got := binary.LittleEndian.Uint32(reply[17:21]); got != want {
			t.Fatalf("%d requested friends: got %d rooms, want %d", size, got, want)
		}
	}
}

func TestCTRFriendResultUnpacksUploadedAttributes(t *testing.T) {
	host := &lobbyConn{ctr: testCTR, replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 89011, player: &playerID{PID: 89011}}
	guest := &lobbyConn{ctr: testCTR, replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 89012, player: &playerID{PID: 89012}}
	defer func() {
		sessionsMu.Lock()
		defer sessionsMu.Unlock()
		for id, s := range sessions {
			if s.ownerPID == host.pid() {
				delete(sessions, id)
			}
		}
	}()
	attrs := &demonware.Writer{}
	attrs.Blob(make([]byte, 8))
	attrs.Blob(make([]byte, 16))
	attrs.String("PARTY-Test")
	attrs.Uint64(host.pid())
	attrs.Uint64(123)
	for i := 0; i < 12; i++ {
		attrs.Uint32(uint32(i))
	}
	attrs.Raw([]byte{0x0d, 0, 0, 0, 0})
	request := &demonware.Writer{}
	request.Blob(make([]byte, 67))
	request.Uint32(8)
	request.Uint32(8)
	request.Blob(attrs.Bytes())
	request.Uint32(999) // request option must not leak into result fields
	host.onMatchMakingContext(mmCreateSession, demonware.NewReader(request.Bytes()), "ctr:attrs-test")
	ask := &demonware.Writer{}
	ask.Uint64(host.pid())
	result := guest.onFriendSessions(14, demonware.NewReader(ask.Bytes()), "ctr:attrs-test")
	r := demonware.NewReader(result[26:])
	r.Blob()
	r.Blob()
	r.Uint32()
	r.Uint32()
	r.Uint32()
	if !bytes.Equal(r.Remaining(), attrs.Bytes()) {
		t.Fatal("CTR attributes retain upload wrapper or request options")
	}
	securityID, err := r.Blob()
	if err != nil || len(securityID) != 8 {
		t.Fatal("client cannot read first result attribute")
	}
}

func TestCTRReservedSessionLifecycle(t *testing.T) {
	host := &lobbyConn{ctr: testCTR, replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 89001, player: &playerID{PID: 89001}}
	guest := &lobbyConn{ctr: testCTR, replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 89002, player: &playerID{PID: 89002}}
	defer func() {
		sessionsMu.Lock()
		defer sessionsMu.Unlock()
		for id, s := range sessions {
			if s.ownerPID == host.pid() {
				delete(sessions, id)
			}
		}
	}()
	reply := host.onMatchMakingContext(mmRequestSessionID, demonware.NewReader(nil), "ctr:reserve-test")
	r := demonware.NewReader(reply[26:])
	sid, err := r.Blob()
	if err != nil || len(sid) != 8 {
		t.Fatal("missing reserved session ID", err)
	}
	lookup := func() uint32 {
		w := &demonware.Writer{}
		w.Uint64(host.pid())
		b := guest.onFriendSessions(14, demonware.NewReader(w.Bytes()), "ctr:reserve-test")
		return binary.LittleEndian.Uint32(b[17:21])
	}
	if lookup() != 0 {
		t.Fatal("uninitialized reservation exposed to friends")
	}
	w := &demonware.Writer{}
	w.Blob(sid)
	w.Blob(make([]byte, 67))
	w.Uint32(1)
	w.Uint32(8)
	bad := guest.onMatchMakingContext(mmInitializeSession, demonware.NewReader(w.Bytes()), "ctr:reserve-test")
	if binary.LittleEndian.Uint32(bad[10:14]) == 0 {
		t.Fatal("non-owner initialized session")
	}
	good := host.onMatchMakingContext(mmInitializeSession, demonware.NewReader(w.Bytes()), "ctr:reserve-test")
	if binary.LittleEndian.Uint32(good[10:14]) != 0 || lookup() != 1 {
		t.Fatal("initialized room not discoverable")
	}
}
