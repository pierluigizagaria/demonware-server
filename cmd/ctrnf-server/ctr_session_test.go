package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

func TestCTRFriendBeyondFirstTwenty(t *testing.T) {
	host := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 90343, Player: &platform.PlayerID{PID: 90343}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	guest := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 99000, Player: &platform.PlayerID{PID: 99000}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer func() {
		testRegistry.Mu.Lock()
		defer testRegistry.Mu.Unlock()
		for id, s := range testRegistry.Sessions {
			if s.OwnerPID == host.PID() {
				delete(testRegistry.Sessions, id)
			}
		}
	}()
	info := &demonware.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	host.onMatchMakingContext(lobby.MMCreateSession, demonware.NewReader(info.Bytes()), "ctr:full-list-test")
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
	host := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89011, Player: &platform.PlayerID{PID: 89011}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	guest := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89012, Player: &platform.PlayerID{PID: 89012}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer func() {
		testRegistry.Mu.Lock()
		defer testRegistry.Mu.Unlock()
		for id, s := range testRegistry.Sessions {
			if s.OwnerPID == host.PID() {
				delete(testRegistry.Sessions, id)
			}
		}
	}()
	attrs := &demonware.Writer{}
	attrs.Blob(make([]byte, 8))
	attrs.Blob(make([]byte, 16))
	attrs.String("PARTY-Test")
	attrs.Uint64(host.PID())
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
	host.onMatchMakingContext(lobby.MMCreateSession, demonware.NewReader(request.Bytes()), "ctr:attrs-test")
	ask := &demonware.Writer{}
	ask.Uint64(host.PID())
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
	host := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89001, Player: &platform.PlayerID{PID: 89001}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	guest := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89002, Player: &platform.PlayerID{PID: 89002}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer func() {
		testRegistry.Mu.Lock()
		defer testRegistry.Mu.Unlock()
		for id, s := range testRegistry.Sessions {
			if s.OwnerPID == host.PID() {
				delete(testRegistry.Sessions, id)
			}
		}
	}()
	reply := host.onMatchMakingContext(lobby.MMRequestSessionID, demonware.NewReader(nil), "ctr:reserve-test")
	r := demonware.NewReader(reply[26:])
	sid, err := r.Blob()
	if err != nil || len(sid) != 8 {
		t.Fatal("missing reserved session ID", err)
	}
	lookup := func() uint32 {
		w := &demonware.Writer{}
		w.Uint64(host.PID())
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
	bad := guest.onMatchMakingContext(lobby.MMInitializeSession, demonware.NewReader(w.Bytes()), "ctr:reserve-test")
	if binary.LittleEndian.Uint32(bad[10:14]) == 0 {
		t.Fatal("non-owner initialized session")
	}
	good := host.onMatchMakingContext(lobby.MMInitializeSession, demonware.NewReader(w.Bytes()), "ctr:reserve-test")
	if binary.LittleEndian.Uint32(good[10:14]) != 0 || lookup() != 1 {
		t.Fatal("initialized room not discoverable")
	}
}
