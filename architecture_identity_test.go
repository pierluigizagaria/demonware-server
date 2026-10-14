package main

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

func TestArchitectureIdentityDoubleRead(t *testing.T) {
	now := time.Now()
	ticket := psnTicket(now)
	issueTestTicket(ticket, playerID{PID: 17, Username: "fixture", Title: 5760})
	l, _ := sharedPS4Lobby()
	l.keys, l.player = nil, nil
	raw := demonware.Frame(append(append([]byte{demonware.MsgClientAuth}, ticket...), make([]byte, 8)...))
	candidates := l.candidates(raw)
	if len(candidates) != 2 {
		t.Fatal("fresh locally issued ticket did not supply candidates")
	}
	// Advance only the pruning call's explicit time, not the process clock.
	// This is the interval between the two reads in onClientAuth. Key proof
	// verification itself has no additional ticket-directory dependency.
	other := psnTicket(now.Add(ticketWindow + issuedIdentityGrace))
	rememberTicket(other, playerID{PID: 17}, now.Add(ticketWindow+issuedIdentityGrace), ticketWindow)
	k := demonware.DeriveKeys([]byte("synthetic verified transcript"), candidates[0].key24)
	l.keys = &k
	l.loadIdentity(ticket)
	if l.keys == nil || l.player != nil {
		t.Fatal("double-read baseline changed")
	}
	if fresh := l.candidates(raw); len(fresh) != 0 {
		t.Fatal("pruned ticket admitted on a new connection")
	}
}

func TestArchitectureAuthSignatureBeforeInspection(t *testing.T) {
	signer := testCTRSigner(t)
	h := newPlatformAuthHandler(fixturePSNPlatform(), signer, "")
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatal("synthetic provider refused")
	}
	signature, err := base64.StdEncoding.DecodeString(w.Header().Get("X-Signature"))
	if err != nil {
		t.Fatal("missing signature")
	}
	digest := sha256.Sum256(w.Body.Bytes())
	if rsa.VerifyPSS(signer.public, crypto.SHA256, digest[:], signature, nil) != nil {
		t.Fatal("original response body is not signed")
	}
	var response map[string]string
	if json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal("invalid signed JSON")
	}
	if len(response) != 6 || response["code"] != "700" || response["auth_task"] != "47" {
		t.Fatal("signed response envelope changed")
	}
	client, err := base64.StdEncoding.DecodeString(response["client_ticket"])
	if err != nil || response["client_ticket"] != response["server_ticket"] {
		t.Fatal("ticket copies changed")
	}
	if _, ok := issuedTicketPlayer(client, time.Now(), ticketWindow); !ok {
		t.Fatal("signed ticket not admitted")
	}
}

func TestArchitectureStatsMissingIdentityAdapter(t *testing.T) {
	l, w, _ := architectureBaseline(t)
	l.player = nil
	l.onTask([]byte{4, 3, 1})
	frames := architectureFrames(t, l, w, 0)
	if len(frames) != 1 {
		t.Fatal("missing stats adapter reply")
	}
	want := demonware.TaskReply(1, 1, 0, nil)
	if frames[0].Payload != hex.EncodeToString(want) {
		t.Fatal("stats adapter no-player empty success changed")
	}
	marketplaceOutcome(t, l.onCTRStats(1, demonware.NewReader(nil)), errUnhandled)
}
