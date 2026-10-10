package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	dir := t.TempDir()
	handler := newPlatformAuthHandler(p, nil, dir, "")
	call := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
		return w
	}
	if w := call(); w.Code != 200 {
		t.Fatal("verified identity refused")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.tkt"))
	if len(files) != 1 {
		t.Fatal("ticket missing")
	}
	ticket, _ := os.ReadFile(files[0])
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
