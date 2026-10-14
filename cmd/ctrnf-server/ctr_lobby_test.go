package main

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
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

// The HTTP code is written into fields 1 AND 2: bdHTTPProxyResponse reads field 2, the REST path
// (bdRESTLSGResponseMessageDeserializer) reads field 1. The body stays field 3.
func TestCTRHTTPProxyReplyEncoding(t *testing.T) {
	body := []byte(`{"enrollments":[]}`)
	reply := testReplies.HTTPProxyReply(1, 200, body)
	i := 16 // transaction + error + task; structured replies have no result counts.
	if len(reply) < i+6 || reply[i] != demonware.TagStruct {
		t.Fatalf("no struct buffer immediately after task header: %x", reply)
	}
	sb := reply[i+6:] // demonware.TagStruct + (demonware.TagUint32 + u32 length)
	want := append([]byte{0x08, 0xC8, 0x01, 0x10, 0xC8, 0x01, 0x1A, byte(len(body))}, body...)
	if !bytes.Equal(sb, want) {
		t.Fatalf("struct buffer\n got %x\nwant %x", sb, want)
	}
}

// A connected friend's presence comes back; a departed player's does not.
func TestCTRRichPresenceRoundTrip(t *testing.T) {
	host := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 7771, Player: &platform.PlayerID{Username: "Mars", PID: 1800000119}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	testPresence.OnlineMu.Lock()
	testPresence.Online[host.N] = host.Player
	testPresence.OnlineMu.Unlock()
	defer testPresence.DropOnline(host.N)

	set := &demonware.Writer{}
	set.String("")
	set.Uint64(1800000119)
	set.String("nintendo")
	set.Uint8(2)
	set.Blob([]byte("racing"))
	host.onRichPresence(presence.RPSet, demonware.NewReader(set.Bytes()))

	ask := func(id uint64) []byte {
		q := &demonware.Writer{}
		q.String("")
		q.Uint32(1)
		q.Uint64(id)
		q.String("nintendo")
		peer := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 7772, Player: &platform.PlayerID{Username: "Collecting", PID: 1800003406}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
		return peer.onRichPresence(presence.RPGetAndSubscribe, demonware.NewReader(q.Bytes()))
	}
	if n := binary.LittleEndian.Uint32(ask(1800000119)[17:21]); n != 1 {
		t.Fatalf("online friend presence missing: %d results", n)
	}
	if rows, ids := rpRows(t, ask(1800999999)); len(rows) != 1 || ids[0] != 1800999999 || rows[0].Flag != 0 || len(rows[0].Data) != 0 {
		t.Fatalf("offline lookup was not resolved safely: %+v", rows)
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
