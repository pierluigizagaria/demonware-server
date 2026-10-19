package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

type platformWire struct{ bytes.Buffer }

func (*platformWire) Read([]byte) (int, error) { return 0, io.EOF }

func (*platformWire) Close() error { return nil }

func (*platformWire) LocalAddr() net.Addr { return &net.TCPAddr{} }

func (*platformWire) RemoteAddr() net.Addr { return &net.TCPAddr{} }

func (*platformWire) SetDeadline(time.Time) error { return nil }

func (*platformWire) SetReadDeadline(time.Time) error { return nil }

func (*platformWire) SetWriteDeadline(time.Time) error { return nil }

func sharedPS4Lobby() (*lobbyConn, *platformWire) {
	wire := &platformWire{}
	l := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 909090, Player: &platform.PlayerID{PID: 17, Username: "fixture", Title: 5760, Kind: "shadnet", Sub: "shadnet:17"}}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	l.C = wire
	return l, wire
}

func sharedTask(t *testing.T, l *lobbyConn, w *platformWire, p []byte) []byte {
	t.Helper()
	w.Reset()
	l.onTask(p)
	frames := marketplaceFrames(t, l, w)
	if len(frames) == 0 || frames[0][0] != demonware.InnerTaskReply {
		t.Fatal("shared dispatcher did not reply")
	}
	return frames[0][1:]
}

func TestSharedPS4DispatcherStartup(t *testing.T) {
	l, w := sharedPS4Lobby()
	report := demonware.NewWriter([]byte{38, demonware.TagUint8, 6})
	report.Uint32(0)
	report.Uint32(0)
	report.Uint64(0)
	report.Uint64(0)
	report.Uint64(0)
	report.Blob(make([]byte, 6))
	report.String("nextendo:shadnet:17")
	report.Raw([]byte{0})
	for _, tc := range []struct {
		name    string
		payload []byte
		want    uint32
	}{
		{"identity report", report.Bytes(), 0},
		{"malformed identity report", []byte{38, demonware.TagUint8, 6, 0}, 1},
		{"existing AB handler", []byte{194, demonware.TagUint8, 1, 0}, 0},
		{"raw bandwidth completion", []byte{18, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 1},
		{"existing matchmaking token", []byte{145, demonware.TagUint8, 3, 0}, 0},
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
	p := ps4.PSNPlatform{Verifier: platformVerifierFunc(func(string, time.Time) (platform.Identity, error) {
		calls++
		if deny {
			return platform.Identity{}, errors.New("refused")
		}
		return platform.Identity{AccountID: 17, Subject: "17", OnlineID: "fixture"}, nil
	})}
	handler := testAuthHandler(p, "")
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
	if _, ok := testTickets.IssuedIdentity(ticket); !ok {
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
