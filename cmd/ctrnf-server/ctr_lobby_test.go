package main

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

// A friend stays joinable while their host reconnects.
func TestCTRFriendSessionSurvivesReconnect(t *testing.T) {
	host := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 5551, Player: &platform.PlayerID{Username: "Mars", PID: 1800000119}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer testRegistry.DropSessionsOf(host.N)
	info := &demonware.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	host.onMatchMakingContext(lobby.MMCreateSession, demonware.NewReader(info.Bytes()), "ctr:Delta")

	testRegistry.DropSessionsOf(host.N) // the host loses its connection

	friend := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 5552, Player: &platform.PlayerID{Username: "Collecting", PID: 1800003406}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	ask := &demonware.Writer{}
	ask.Uint64(1800000119)
	reply := friend.onFriendSessions(14, demonware.NewReader(ask.Bytes()), "ctr:Delta")
	if n := binary.LittleEndian.Uint32(reply[17:21]); n != 1 {
		t.Fatalf("orphaned host session not offered to friend: %d results", n)
	}

	testRegistry.Mu.Lock()
	for _, s := range testRegistry.Sessions {
		if s.OwnerPID == 1800000119 {
			s.Orphaned = time.Now().Add(-2 * lobby.OrphanGrace)
		}
	}
	testRegistry.Mu.Unlock()
	testRegistry.ReapSessionsOnce()
	reply = friend.onFriendSessions(14, demonware.NewReader(ask.Bytes()), "ctr:Delta")
	if n := binary.LittleEndian.Uint32(reply[17:21]); n != 0 {
		t.Fatalf("abandoned session still offered: %d results", n)
	}
}

func TestCTRSessionCreateDelete(t *testing.T) {
	l := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 987654}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer testRegistry.DropSessionsOf(l.N)
	info := &demonware.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	info.Blob([]byte("CTR attributes"))
	response := l.onMatchMakingContext(1, demonware.NewReader(info.Bytes()), "ctr:Delta")
	if len(response) < 26 || binary.LittleEndian.Uint32(response[17:21]) != 1 {
		t.Fatalf("create must return one session ID: %x", response)
	}
	var session *lobby.MatchSession
	testRegistry.Mu.Lock()
	for _, s := range testRegistry.Sessions {
		if s.Owner == l.N {
			session = s
			break
		}
	}
	testRegistry.Mu.Unlock()
	if session == nil || session.Context != "ctr:Delta" {
		t.Fatal("missing stored CTR session")
	}
	args := &demonware.Writer{}
	args.Blob(session.ID[:])
	other := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 987655}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	other.onMatchMakingContext(3, demonware.NewReader(args.Bytes()), "ctr:Delta")
	testRegistry.Mu.Lock()
	_, exists := testRegistry.Sessions[session.ID]
	testRegistry.Mu.Unlock()
	if !exists {
		t.Fatal("another connection deleted the session")
	}
	l.onMatchMakingContext(3, demonware.NewReader(args.Bytes()), "ctr:Delta")
	testRegistry.Mu.Lock()
	_, exists = testRegistry.Sessions[session.ID]
	testRegistry.Mu.Unlock()
	if exists {
		t.Fatal("owner deletion did not remove the session")
	}
}
