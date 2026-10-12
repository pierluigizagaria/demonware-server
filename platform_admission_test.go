package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/iotest"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/platform"
)

func testCTRSigner(t *testing.T) *ctrAuthSigner {
	t.Helper()
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl unavailable")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signer.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadCTRAuthSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fixturePSNPlatform() psnPlatform {
	return psnPlatform{verifier: platformVerifierFunc(func(string, time.Time) (platform.Identity, error) {
		return platform.Identity{AccountID: 17, Subject: "17", OnlineID: "fixture"}, nil
	})}
}

func psnTicket(issued time.Time) []byte {
	ticket := make([]byte, 128)
	binary.LittleEndian.PutUint32(ticket, ticketMagic)
	ticket[4] = 1
	binary.LittleEndian.PutUint32(ticket[5:], 5760)
	binary.LittleEndian.PutUint32(ticket[13:], uint32(issued.Unix()))
	binary.LittleEndian.PutUint64(ticket[17:], 17)
	binary.LittleEndian.PutUint64(ticket[25:], 17)
	_, _ = rand.Read(ticket[33:97])
	copy(ticket[97:121], ticket[33:57])
	return ticket
}

// handshake sends a client 0x82 carrying ticket, tagged with the ticket's own
// lobby key, and returns the connection so callers can see if it was keyed.
func handshake(t *testing.T, ticket []byte) *lobbyConn {
	t.Helper()
	l, _ := sharedPS4Lobby()
	l.keys, l.player = nil, nil
	l.maxLen, l.nonce, l.payload1 = 65536, make([]byte, 8), make([]byte, 20)
	raw := frame(append(append([]byte{msgClientAuth}, ticket...), make([]byte, 8)...))
	var transcript bytes.Buffer
	transcript.Write(u32le(210))
	transcript.Write(u32le(220))
	transcript.Write(u32le(l.maxLen))
	transcript.Write(l.nonce)
	transcript.Write(u32le(uint32(len(l.payload1) + 2)))
	transcript.WriteByte(frameFlag)
	transcript.WriteByte(msgServerHi)
	transcript.Write(l.payload1)
	transcript.Write(raw[:len(raw)-8])
	k := deriveKeys(transcript.Bytes(), ticket[97:121])
	copy(raw[len(raw)-8:], k.clientTag[:])
	l.onClientAuth(raw)
	return l
}

func TestPS4LobbyAdmitsOnlyFreshIssuedTickets(t *testing.T) {
	if l := handshake(t, psnTicket(time.Now())); l.keys != nil {
		t.Fatal("unissued self-supplied ticket established encrypted session")
	}
	fresh := psnTicket(time.Now())
	issueTestTicket(fresh, playerID{PID: 17, Username: "fixture", Sub: "shadnet:17", Kind: "shadnet", Title: 5760})
	l := handshake(t, fresh)
	defer dropOnline(l.n)
	if l.keys == nil || l.player == nil || l.player.PID != 17 || l.player.Title != 5760 {
		t.Fatal("fresh issued ticket refused or identity lost")
	}
	stale := psnTicket(time.Now().Add(-121 * time.Second))
	issueTestTicket(stale, playerID{PID: 17, Username: "fixture"})
	if l := handshake(t, stale); l.keys != nil {
		t.Fatal("expired publisher ticket admitted")
	}
	foreign := psnTicket(time.Now())
	issueTestTicket(foreign, playerID{PID: 18, Username: "other"})
	if l := handshake(t, foreign); l.keys != nil {
		t.Fatal("ticket bound to another account admitted")
	}
}

func TestPS4AuthRequiresResponseSigner(t *testing.T) {
	h := newPlatformAuthHandler(fixturePSNPlatform(), nil, "")
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	before := issuedTicketCount()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
	if w.Code == 200 {
		t.Fatal("PS4 auth succeeded without required response signature")
	}
	if issuedTicketCount() != before {
		t.Fatal("ticket issued without a signed response")
	}
}

func TestPS4UmbrellaUsesFreshIssuedTicket(t *testing.T) {
	call := func(ticket []byte) int {
		body, _ := json.Marshal(map[string]any{"ticket": ticket, "titleID": "5760"})
		w := httptest.NewRecorder()
		handleCTRUmbrella(w, httptest.NewRequest("POST", "/v1.0/tokens/lsg/", bytes.NewReader(body)), body, 1, "")
		return w.Code
	}
	fresh, stale := psnTicket(time.Now()), psnTicket(time.Now().Add(-121*time.Second))
	for _, ticket := range [][]byte{fresh, stale} {
		issueTestTicket(ticket, playerID{PID: 17, Username: "fixture"})
	}
	if call(fresh) != 200 || call(stale) != 401 || call(psnTicket(time.Now())) != 401 {
		t.Fatal("Umbrella did not apply fresh issued-ticket admission")
	}
}

func TestSharedGetUserIDsResolvesOnlineAndKnownIdentities(t *testing.T) {
	l, w := sharedPS4Lobby()
	withTestPlayerNames(t)
	onlineMu.Lock()
	online[l.n] = l.player
	onlineMu.Unlock()
	defer dropOnline(l.n)
	if err := rememberPlayerName(playerID{PID: 42, Username: "OfflineFriend", Kind: "shadnet"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	req := &bdWriter{b: []byte{12, tagU8, 10}}
	req.u32(3)
	req.strv("FIXTURE")
	req.strv("offlinefriend")
	req.strv("stranger")
	req.b = append(req.b, 0)
	reply := sharedTask(t, l, w, req.b)
	if len(reply) < 21 || binary.LittleEndian.Uint32(reply[10:14]) != 0 || binary.LittleEndian.Uint32(reply[17:21]) != 2 {
		t.Fatal("online and previously authenticated friends were not both resolved")
	}
	if !bytes.Contains(reply, []byte("OfflineFriend\x00")) || bytes.Contains(reply, []byte("stranger")) {
		t.Fatal("wrong identities resolved")
	}
}

func TestCTRFollowerLeaveKeepsPartyAndNotifiesHost(t *testing.T) {
	m := &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
	r := fillTestRoom(t, m, 30000, 2)
	leader := r.players[1]
	leader.doc.Members = []uint64{leader.pid, 30009}
	leader.seats = 2
	follower := &lobbyConn{n: 30009, player: &playerID{PID: 30009}}
	version := r.version
	out := m.leave(follower, 0)
	if len(out) != 1 || out[0].conn != r.host.conn || out[0].event != ctrUpdateLobby || r.version != version+1 {
		t.Fatal("host not notified of the follower's departure")
	}
	if got := r.players[1].doc.Members; len(got) != 1 || got[0] != leader.pid || r.occupancy() != 2 {
		t.Fatal("follower seat not released")
	}
	if out := m.leave(leader.conn, 0); len(out) != 1 || len(r.players) != 1 {
		t.Fatal("leader departure did not remove its search")
	}
}

func TestAuthRefusesIncompleteBody(t *testing.T) {
	h := newPlatformAuthHandler(fixturePSNPlatform(), testCTRSigner(t), "")
	before := issuedTicketCount()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", iotest.ErrReader(io.ErrUnexpectedEOF)))
	if w.Code != 400 {
		t.Fatalf("incomplete body answered %d", w.Code)
	}
	if issuedTicketCount() != before {
		t.Fatal("ticket issued for an incomplete request")
	}
}
