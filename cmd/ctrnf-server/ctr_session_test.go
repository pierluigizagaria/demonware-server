package main

import (
	bytes "bytes"
	binary "encoding/binary"
	demonwarepkg "github.com/pierluigizagaria/demonware-server/internal/demonware"
	lobbypkg "github.com/pierluigizagaria/demonware-server/internal/lobby"
	platformpkg "github.com/pierluigizagaria/demonware-server/internal/platform"
	testing "testing"
)

func TestCTRFriendBeyondFirstTwenty(t *testing.T) {
	host := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 90343, Player: &platformpkg.PlayerID{PID: 90343}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	guest := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 99000, Player: &platformpkg.PlayerID{PID: 99000}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer func() {
		testRegistry.Mu.Lock()
		defer testRegistry.Mu.Unlock()
		for id, s := range testRegistry.Sessions {
			if s.OwnerPID == host.Pid() {
				delete(testRegistry.Sessions, id)
			}
		}
	}()
	info := &demonwarepkg.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	host.onMatchMakingContext(lobbypkg.MmCreateSession, demonwarepkg.NewReader(info.Bytes()), "ctr:full-list-test")
	for _, size := range []int{20, 80} {
		ids := &demonwarepkg.Writer{}
		for i := 1; i <= size; i++ {
			ids.Uint64(uint64(90300 + i))
		}
		reply := guest.onFriendSessions(14, demonwarepkg.NewReader(ids.Bytes()), "ctr:full-list-test")
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
	host := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89011, Player: &platformpkg.PlayerID{PID: 89011}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	guest := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89012, Player: &platformpkg.PlayerID{PID: 89012}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer func() {
		testRegistry.Mu.Lock()
		defer testRegistry.Mu.Unlock()
		for id, s := range testRegistry.Sessions {
			if s.OwnerPID == host.Pid() {
				delete(testRegistry.Sessions, id)
			}
		}
	}()
	attrs := &demonwarepkg.Writer{}
	attrs.Blob(make([]byte, 8))
	attrs.Blob(make([]byte, 16))
	attrs.String("PARTY-Test")
	attrs.Uint64(host.Pid())
	attrs.Uint64(123)
	for i := 0; i < 12; i++ {
		attrs.Uint32(uint32(i))
	}
	attrs.Raw([]byte{0x0d, 0, 0, 0, 0})
	request := &demonwarepkg.Writer{}
	request.Blob(make([]byte, 67))
	request.Uint32(8)
	request.Uint32(8)
	request.Blob(attrs.Bytes())
	request.Uint32(999) // request option must not leak into result fields
	host.onMatchMakingContext(lobbypkg.MmCreateSession, demonwarepkg.NewReader(request.Bytes()), "ctr:attrs-test")
	ask := &demonwarepkg.Writer{}
	ask.Uint64(host.Pid())
	result := guest.onFriendSessions(14, demonwarepkg.NewReader(ask.Bytes()), "ctr:attrs-test")
	r := demonwarepkg.NewReader(result[26:])
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
	host := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89001, Player: &platformpkg.PlayerID{PID: 89001}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	guest := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 89002, Player: &platformpkg.PlayerID{PID: 89002}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer func() {
		testRegistry.Mu.Lock()
		defer testRegistry.Mu.Unlock()
		for id, s := range testRegistry.Sessions {
			if s.OwnerPID == host.Pid() {
				delete(testRegistry.Sessions, id)
			}
		}
	}()
	reply := host.onMatchMakingContext(lobbypkg.MmRequestSessionID, demonwarepkg.NewReader(nil), "ctr:reserve-test")
	r := demonwarepkg.NewReader(reply[26:])
	sid, err := r.Blob()
	if err != nil || len(sid) != 8 {
		t.Fatal("missing reserved session ID", err)
	}
	lookup := func() uint32 {
		w := &demonwarepkg.Writer{}
		w.Uint64(host.Pid())
		b := guest.onFriendSessions(14, demonwarepkg.NewReader(w.Bytes()), "ctr:reserve-test")
		return binary.LittleEndian.Uint32(b[17:21])
	}
	if lookup() != 0 {
		t.Fatal("uninitialized reservation exposed to friends")
	}
	w := &demonwarepkg.Writer{}
	w.Blob(sid)
	w.Blob(make([]byte, 67))
	w.Uint32(1)
	w.Uint32(8)
	bad := guest.onMatchMakingContext(lobbypkg.MmInitializeSession, demonwarepkg.NewReader(w.Bytes()), "ctr:reserve-test")
	if binary.LittleEndian.Uint32(bad[10:14]) == 0 {
		t.Fatal("non-owner initialized session")
	}
	good := host.onMatchMakingContext(lobbypkg.MmInitializeSession, demonwarepkg.NewReader(w.Bytes()), "ctr:reserve-test")
	if binary.LittleEndian.Uint32(good[10:14]) != 0 || lookup() != 1 {
		t.Fatal("initialized room not discoverable")
	}
}
