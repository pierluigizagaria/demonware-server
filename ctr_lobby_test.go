package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
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
	issueTestTicket(ticket, playerID{PID: 17, Username: "fixture"})
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
	host := &lobbyConn{replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 5551, player: &playerID{Username: "Mars", PID: 1800000119}}
	defer dropSessionsOf(host.n)
	info := &demonware.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	host.onMatchMakingContext(mmCreateSession, demonware.NewReader(info.Bytes()), "ctr:Delta")

	dropSessionsOf(host.n) // the host loses its connection

	friend := &lobbyConn{replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 5552, player: &playerID{Username: "Collecting", PID: 1800003406}}
	ask := &demonware.Writer{}
	ask.Uint64(1800000119)
	reply := friend.onFriendSessions(14, demonware.NewReader(ask.Bytes()), "ctr:Delta")
	if n := binary.LittleEndian.Uint32(reply[17:21]); n != 1 {
		t.Fatalf("orphaned host session not offered to friend: %d results", n)
	}

	sessionsMu.Lock()
	for _, s := range sessions {
		if s.ownerPID == 1800000119 {
			s.orphaned = time.Now().Add(-2 * orphanGrace)
		}
	}
	sessionsMu.Unlock()
	reapSessionsOnce()
	reply = friend.onFriendSessions(14, demonware.NewReader(ask.Bytes()), "ctr:Delta")
	if n := binary.LittleEndian.Uint32(reply[17:21]); n != 0 {
		t.Fatalf("abandoned session still offered: %d results", n)
	}
}

// The HTTP code is written into fields 1 AND 2: bdHTTPProxyResponse reads field 2, the REST path
// (bdRESTLSGResponseMessageDeserializer) reads field 1. The body stays field 3.
func TestCTRHTTPProxyReplyEncoding(t *testing.T) {
	body := []byte(`{"enrollments":[]}`)
	reply := testReplies.httpProxyReply(1, 200, body)
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
	host := &lobbyConn{replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 7771, player: &playerID{Username: "Mars", PID: 1800000119}}
	testPresence.onlineMu.Lock()
	testPresence.online[host.n] = host.player
	testPresence.onlineMu.Unlock()
	defer testPresence.dropOnline(host.n)

	set := &demonware.Writer{}
	set.String("")
	set.Uint64(1800000119)
	set.String("nintendo")
	set.Uint8(2)
	set.Blob([]byte("racing"))
	host.onRichPresence(rpSet, demonware.NewReader(set.Bytes()))

	ask := func(id uint64) []byte {
		q := &demonware.Writer{}
		q.String("")
		q.Uint32(1)
		q.Uint64(id)
		q.String("nintendo")
		peer := &lobbyConn{replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 7772, player: &playerID{Username: "Collecting", PID: 1800003406}}
		return peer.onRichPresence(rpGetAndSubscribe, demonware.NewReader(q.Bytes()))
	}
	if n := binary.LittleEndian.Uint32(ask(1800000119)[17:21]); n != 1 {
		t.Fatalf("online friend presence missing: %d results", n)
	}
	if rows, ids := rpRows(t, ask(1800999999)); len(rows) != 1 || ids[0] != 1800999999 || rows[0].flag != 0 || len(rows[0].data) != 0 {
		t.Fatalf("offline lookup was not resolved safely: %+v", rows)
	}
}

func TestCTRSessionCreateDelete(t *testing.T) {
	l := &lobbyConn{replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 987654}
	defer dropSessionsOf(l.n)
	info := &demonware.Writer{}
	info.Blob(make([]byte, 67))
	info.Uint32(8)
	info.Uint32(8)
	info.Blob([]byte("CTR attributes"))
	response := l.onMatchMakingContext(1, demonware.NewReader(info.Bytes()), "ctr:Delta")
	if len(response) < 26 || binary.LittleEndian.Uint32(response[17:21]) != 1 {
		t.Fatalf("create must return one session ID: %x", response)
	}
	var session *mmSession
	sessionsMu.Lock()
	for _, s := range sessions {
		if s.owner == l.n {
			session = s
			break
		}
	}
	sessionsMu.Unlock()
	if session == nil || session.context != "ctr:Delta" {
		t.Fatal("missing stored CTR session")
	}
	args := &demonware.Writer{}
	args.Blob(session.id[:])
	other := &lobbyConn{replyCounter: testReplies, presence: testPresence, tickets: testTickets, n: 987655}
	other.onMatchMakingContext(3, demonware.NewReader(args.Bytes()), "ctr:Delta")
	sessionsMu.Lock()
	_, exists := sessions[session.id]
	sessionsMu.Unlock()
	if !exists {
		t.Fatal("another connection deleted the session")
	}
	l.onMatchMakingContext(3, demonware.NewReader(args.Bytes()), "ctr:Delta")
	sessionsMu.Lock()
	_, exists = sessions[session.id]
	sessionsMu.Unlock()
	if exists {
		t.Fatal("owner deletion did not remove the session")
	}
}
