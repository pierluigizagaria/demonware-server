package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type platformWire struct{ bytes.Buffer }

func (*platformWire) Read([]byte) (int, error)         { return 0, io.EOF }
func (*platformWire) Close() error                     { return nil }
func (*platformWire) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*platformWire) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*platformWire) SetDeadline(time.Time) error      { return nil }
func (*platformWire) SetReadDeadline(time.Time) error  { return nil }
func (*platformWire) SetWriteDeadline(time.Time) error { return nil }
func sharedPS4Lobby() (*lobbyConn, *platformWire) {
	wire := &platformWire{}
	k := deriveKeys([]byte("synthetic transcript"), make([]byte, 24))
	return &lobbyConn{n: 909090, c: wire, keys: &k, player: &playerID{PID: 17, Username: "fixture", Title: 5760, Kind: "shadnet", Sub: "shadnet:17"}}, wire
}
func sharedTask(t *testing.T, l *lobbyConn, w *platformWire, p []byte) []byte {
	t.Helper()
	w.Reset()
	l.onTask(p)
	raw := w.Bytes()
	if len(raw) < 50 {
		t.Fatal("shared dispatcher did not reply")
	}
	plain, err := aesCBCDecrypt(l.keys.s2cAES, raw[10:26], raw[26:len(raw)-8])
	if err != nil {
		t.Fatal(err)
	}
	n := binary.LittleEndian.Uint32(plain)
	if int(n) > len(plain)-5 {
		t.Fatal("bad response frame")
	}
	return plain[5 : 5+n]
}
func TestSharedPS4DispatcherStartup(t *testing.T) {
	l, w := sharedPS4Lobby()
	report := &bdWriter{b: []byte{38, tagU8, 6}}
	report.u32(0)
	report.u32(0)
	report.u64(0)
	report.u64(0)
	report.u64(0)
	report.blobv(make([]byte, 6))
	report.strv("nextendo:shadnet:17")
	report.b = append(report.b, 0)
	for _, tc := range []struct {
		name    string
		payload []byte
		want    uint32
	}{
		{"identity report", report.b, 0},
		{"malformed identity report", []byte{38, tagU8, 6, 0}, 1},
		{"existing AB handler", []byte{194, tagU8, 1, 0}, 0},
		{"raw bandwidth completion", []byte{18, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 1},
		{"existing matchmaking token", []byte{145, tagU8, 3, 0}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := sharedTask(t, l, w, tc.payload)
			if len(reply) < 14 || binary.LittleEndian.Uint32(reply[10:14]) != tc.want {
				t.Fatal("wrong task outcome")
			}
		})
	}
}
func TestSharedPS4AuthUsesInjectedVerifier(t *testing.T) {
	calls := 0
	deny := false
	p := psnPlatform{verifier: platformVerifierFunc(func(string, time.Time) (platform.Identity, error) {
		calls++
		if deny {
			return platform.Identity{}, errors.New("refused")
		}
		return platform.Identity{AccountID: 17, Subject: "17", OnlineID: "fixture"}, nil
	})}
	handler := newPlatformAuthHandler(p, testCTRSigner(t), "")
	call := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
		return w
	}
	w := call()
	if w.Code != 200 {
		t.Fatal("verified identity refused")
	}
	var response struct {
		ServerTicket string `json:"server_ticket"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	ticket, _ := base64.StdEncoding.DecodeString(response.ServerTicket)
	if _, ok := issuedIdentity(ticket); !ok {
		t.Fatal("ticket missing")
	}
	if len(ticket) != 128 || binary.LittleEndian.Uint32(ticket[5:9]) != 5760 || binary.LittleEndian.Uint64(ticket[25:33]) != 17 {
		t.Fatal("PS4 ticket identity/title lost")
	}
	deny = true
	if w := call(); w.Code != 403 {
		t.Fatal("failed verification issued a ticket")
	}
	if calls != 2 {
		t.Fatal("injected verifier not used for every admission")
	}
}
func TestGlobalRedactionDefault(t *testing.T) {
	if !redactLogs {
		t.Skip("explicit diagnostic opt-out")
	}
	if d := runDumpDir("platform-redaction-test"); d != "" {
		t.Fatal("raw dumps enabled under global redaction")
	}
}

func TestRedactedLogsKeepNumbersAndHideSecrets(t *testing.T) {
	if !redactLogs {
		t.Skip("explicit diagnostic opt-out")
	}
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	diagnosticLogf("task=%d ok=%t key=%X name=%q", 145, true, []byte{0xAB, 0xCD}, "player")
	if got := out.String(); !strings.Contains(got, "task=145 ok=true key=<redacted> name=<redacted>") || strings.Contains(got, "ABCD") || strings.Contains(got, "player") {
		t.Fatal("unexpected redaction: " + got)
	}
	out.Reset()
	type hit struct {
		id   uint64
		name string
	}
	addr := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 3074}
	err := &net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: errors.New("reset")}
	diagnosticLogf("hits=%v map=%v from=%s err=%v", []hit{{1, "player"}}, map[string]int{"player": 1}, addr, err)
	if got := out.String(); strings.Contains(got, "player") || strings.Contains(got, "203.0.113.7") || !strings.Contains(got, "err=<*net.OpError>") {
		t.Fatal("structured values or error text leaked: " + got)
	}
}

// clientFrame encrypts one client 0x85 frame carrying a task request.
func clientFrame(t *testing.T, l *lobbyConn, seq uint32, task []byte) []byte {
	t.Helper()
	pt := binary.LittleEndian.AppendUint32(nil, uint32(len(task)))
	pt = append(pt, 0x86)
	pt = append(pt, task...)
	for len(pt)%16 != 0 {
		pt = append(pt, 0)
	}
	iv := make([]byte, 16)
	ct, err := aesCBCEncrypt(l.keys.c2sAES, iv, pt)
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte{msgEncrypted}, binary.LittleEndian.AppendUint32(nil, seq)...)
	body = append(append(body, iv...), ct...)
	out := binary.LittleEndian.AppendUint32(nil, uint32(1+len(body)+8))
	out = append(append(out, frameFlag), body...)
	return append(out, hmacSHA1(l.keys.c2sMAC, out)[:8]...)
}

func TestEncryptedFramesFollowClientSequence(t *testing.T) {
	l, w := sharedPS4Lobby()
	task := (&bdWriter{b: []byte{6, tagU8, 1}}).b // any request; the reply only needs to exist
	replied := func(raw []byte) bool {
		w.Reset()
		l.onEncrypted(raw)
		return w.Len() > 0
	}
	first := clientFrame(t, l, 1, task)
	if !replied(first) {
		t.Fatal("first frame not processed")
	}
	if replied(first) {
		t.Fatal("replayed frame processed")
	}
	if replied(clientFrame(t, l, 3, task)) {
		t.Fatal("frame ahead of the sequence processed")
	}
	if !replied(clientFrame(t, l, 2, task)) {
		t.Fatal("next genuine frame refused after a dropped one")
	}
}
