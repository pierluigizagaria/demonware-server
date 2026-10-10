//go:build acceptance

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	"net/http/httptest"
	"testing"
	"time"
)

// These deployment gates intentionally expose limitations of the bounded experiment.
func TestAcceptanceRejectUnissuedLobbyTicket(t *testing.T) {
	l, _ := sharedPS4Lobby()
	l.keys = nil
	l.player = nil
	l.sessDir = t.TempDir()
	l.maxLen = 65536
	l.nonce = make([]byte, 8)
	l.payload1 = make([]byte, 20)
	ticket := make([]byte, 128)
	binary.LittleEndian.PutUint32(ticket, ticketMagic)
	binary.LittleEndian.PutUint32(ticket[5:], 5760)
	binary.LittleEndian.PutUint64(ticket[25:], 17)
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
	if l.keys != nil {
		t.Fatal("unissued self-supplied ticket established encrypted session")
	}
}
func TestAcceptanceRequirePS4ResponseSigner(t *testing.T) {
	p := psnPlatform{verifier: platformVerifierFunc(func(string, time.Time) (platform.Identity, error) {
		return platform.Identity{AccountID: 17, Subject: "17", OnlineID: "fixture"}, nil
	})}
	h := newPlatformAuthHandler(p, nil, t.TempDir(), "")
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
	if w.Code == 200 {
		t.Fatal("PS4 auth succeeded without required response signature")
	}
}
func TestAcceptanceResolveOnlineFriendDuringStartup(t *testing.T) {
	l, w := sharedPS4Lobby()
	onlineMu.Lock()
	online[l.n] = l.player
	onlineMu.Unlock()
	defer dropOnline(l.n)
	req := &bdWriter{b: []byte{12, tagU8, 10}}
	req.u32(1)
	req.strv("fixture")
	req.b = append(req.b, 0)
	reply := sharedTask(t, l, w, req.b)
	if len(reply) < 21 || binary.LittleEndian.Uint32(reply[10:14]) != 0 || binary.LittleEndian.Uint32(reply[17:21]) != 1 {
		t.Fatal("known online friend did not resolve through shared 12/10 handler")
	}
}
