package main

import (
	bytes "bytes"
	base64 "encoding/base64"
	binary "encoding/binary"
	json "encoding/json"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
	httptest "net/http/httptest"
	testing "testing"
	time "time"
)

func TestCTRUmbrellaIssuedTicket(t *testing.T) {
	ticket := psnTicket(time.Now())
	call := func(titleID any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"ticket": base64.StdEncoding.EncodeToString(ticket), "titleID": titleID})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1.0/tokens/lsg/", bytes.NewReader(body))
		testUmbrellaHandler(w, r, body, 1, "")
		return w
	}
	if w := call(5760); w.Code != 401 {
		t.Fatalf("unissued ticket accepted: %d", w.Code)
	}
	issueTestTicket(ticket, platformpkg.PlayerID{PID: 17, Username: "fixture"})
	// The client sends its integers as JSON strings, so both forms must pass.
	if w := call("5760"); w.Code != 200 {
		t.Fatalf("string-encoded titleID rejected: %d %s", w.Code, w.Body)
	}
	w := call(5760)
	if w.Code != 200 {
		t.Fatalf("issued ticket rejected: %d", w.Code)
	}
	var result struct {
		ID       uint64 `json:"umbrellaID"`
		Token    string `json:"accessToken"`
		Expires  int64  `json:"expires"`
		Accounts []any  `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != 17 || result.Token == "" || result.Expires <= time.Now().Unix() || result.Accounts == nil {
		t.Fatal("incomplete CTR account response")
	}
}

// A friend stays joinable while their host reconnects.
func TestCTRFriendSessionSurvivesReconnect(t *testing.T) {
	host := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 5551, Player: &platformpkg.PlayerID{Username: "Mars", PID: 1800000119}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer testRegistry.DropSessionsOf(host.N)
	info := &demonwarepkg.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	host.onMatchMakingContext(lobbypkg.MmCreateSession, demonwarepkg.NewReader(info.Bytes()), "ctr:Delta")

	testRegistry.DropSessionsOf(host.N) // the host loses its connection

	friend := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 5552, Player: &platformpkg.PlayerID{Username: "Collecting", PID: 1800003406}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	ask := &demonwarepkg.Writer{}
	ask.Uint64(1800000119)
	reply := friend.onFriendSessions(14, demonwarepkg.NewReader(ask.Bytes()), "ctr:Delta")
	if n := binary.LittleEndian.Uint32(reply[17:21]); n != 1 {
		t.Fatalf("orphaned host session not offered to friend: %d results", n)
	}

	testRegistry.Mu.Lock()
	for _, s := range testRegistry.Sessions {
		if s.OwnerPID == 1800000119 {
			s.Orphaned = time.Now().Add(-2 * lobbypkg.OrphanGrace)
		}
	}
	testRegistry.Mu.Unlock()
	testRegistry.ReapSessionsOnce()
	reply = friend.onFriendSessions(14, demonwarepkg.NewReader(ask.Bytes()), "ctr:Delta")
	if n := binary.LittleEndian.Uint32(reply[17:21]); n != 0 {
		t.Fatalf("abandoned session still offered: %d results", n)
	}
}

// The HTTP code is written into fields 1 AND 2: bdHTTPProxyResponse reads field 2, the REST path
// (bdRESTLSGResponseMessageDeserializer) reads field 1. The body stays field 3.
func TestCTRHTTPProxyReplyEncoding(t *testing.T) {
	body := []byte(`{"enrollments":[]}`)
	reply := testReplies.HttpProxyReply(1, 200, body)
	i := 16 // transaction + error + task; structured replies have no result counts.
	if len(reply) < i+6 || reply[i] != demonwarepkg.TagStruct {
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
	host := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 7771, Player: &platformpkg.PlayerID{Username: "Mars", PID: 1800000119}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	testPresence.OnlineMu.Lock()
	testPresence.Online[host.N] = host.Player
	testPresence.OnlineMu.Unlock()
	defer testPresence.DropOnline(host.N)

	set := &demonwarepkg.Writer{}
	set.String("")
	set.Uint64(1800000119)
	set.String("nintendo")
	set.Uint8(2)
	set.Blob([]byte("racing"))
	host.onRichPresence(presencepkg.RpSet, demonwarepkg.NewReader(set.Bytes()))

	ask := func(id uint64) []byte {
		q := &demonwarepkg.Writer{}
		q.String("")
		q.Uint32(1)
		q.Uint64(id)
		q.String("nintendo")
		peer := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 7772, Player: &platformpkg.PlayerID{Username: "Collecting", PID: 1800003406}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
		return peer.onRichPresence(presencepkg.RpGetAndSubscribe, demonwarepkg.NewReader(q.Bytes()))
	}
	if n := binary.LittleEndian.Uint32(ask(1800000119)[17:21]); n != 1 {
		t.Fatalf("online friend presence missing: %d results", n)
	}
	if rows, ids := rpRows(t, ask(1800999999)); len(rows) != 1 || ids[0] != 1800999999 || rows[0].Flag != 0 || len(rows[0].Data) != 0 {
		t.Fatalf("offline lookup was not resolved safely: %+v", rows)
	}
}

func TestCTRSessionCreateDelete(t *testing.T) {
	l := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 987654}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	defer testRegistry.DropSessionsOf(l.N)
	info := &demonwarepkg.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	info.Blob([]byte("CTR attributes"))
	response := l.onMatchMakingContext(1, demonwarepkg.NewReader(info.Bytes()), "ctr:Delta")
	if len(response) < 26 || binary.LittleEndian.Uint32(response[17:21]) != 1 {
		t.Fatalf("create must return one session ID: %x", response)
	}
	var session *lobbypkg.MmSession
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
	args := &demonwarepkg.Writer{}
	args.Blob(session.Id[:])
	other := newTestLobby(&lobbypkg.LobbySession{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 987655}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	other.onMatchMakingContext(3, demonwarepkg.NewReader(args.Bytes()), "ctr:Delta")
	testRegistry.Mu.Lock()
	_, exists := testRegistry.Sessions[session.Id]
	testRegistry.Mu.Unlock()
	if !exists {
		t.Fatal("another connection deleted the session")
	}
	l.onMatchMakingContext(3, demonwarepkg.NewReader(args.Bytes()), "ctr:Delta")
	testRegistry.Mu.Lock()
	_, exists = testRegistry.Sessions[session.Id]
	testRegistry.Mu.Unlock()
	if exists {
		t.Fatal("owner deletion did not remove the session")
	}
}
