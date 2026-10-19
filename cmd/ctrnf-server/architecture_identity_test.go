package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func TestArchitectureIdentityDoubleRead(t *testing.T) {
	now := time.Now()
	ticket := psnTicket(now)
	issueTestTicket(ticket, platform.PlayerID{PID: 17, Username: "fixture", Title: 5760})
	l, _ := sharedPS4Lobby()
	l.Player = nil
	raw := demonware.Frame(append(append([]byte{demonware.MsgClientAuth}, ticket...), make([]byte, 8)...))
	candidates := l.Candidates(raw)
	if len(candidates) != 2 {
		t.Fatal("fresh locally issued ticket did not supply candidates")
	}
	// Advance only the pruning call's explicit time, not the process clock.
	// This is the interval between the two reads in onClientAuth. Key proof
	// verification itself has no additional ticket-directory dependency.
	other := psnTicket(now.Add(ps4.TicketWindow + auth.IssuedIdentityGrace))
	testTickets.RememberTicket(other, platform.PlayerID{PID: 17}, now.Add(ps4.TicketWindow+auth.IssuedIdentityGrace), ps4.TicketWindow)
	// The fixture has already established a key through a valid proof.
	// Pruning cannot clear it when the second identity lookup now fails.
	l.LoadIdentity(ticket)
	if !l.ChannelSnapshot().Established || l.Player != nil {
		t.Fatal("double-read baseline changed")
	}
	if fresh := l.Candidates(raw); len(fresh) != 0 {
		t.Fatal("pruned ticket admitted on a new connection")
	}
}

func TestArchitectureAuthReplyIsUnsigned(t *testing.T) {
	h := testAuthHandler(fixturePSNPlatform(), "")
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatal("synthetic provider refused")
	}
	// The signature-patched client ignores the verifier's failure; no key is held.
	if _, signed := w.Header()["X-Signature"]; signed {
		t.Fatal("reply carries a response signature")
	}
	var response map[string]string
	if json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal("invalid response JSON")
	}
	if len(response) != 6 || response["code"] != "700" || response["auth_task"] != "47" {
		t.Fatal("response envelope changed")
	}
	client, err := base64.StdEncoding.DecodeString(response["client_ticket"])
	if err != nil || response["client_ticket"] != response["server_ticket"] {
		t.Fatal("ticket copies changed")
	}
	if _, ok := testTickets.IssuedTicketPlayer(client, time.Now(), ps4.TicketWindow); !ok {
		t.Fatal("signed ticket not admitted")
	}
}

func TestArchitectureStatsMissingIdentityAdapter(t *testing.T) {
	l, w, _ := architectureBaseline(t)
	l.Player = nil
	l.onTask([]byte{4, 3, 1})
	frames := architectureFrames(t, l, w, 0)
	if len(frames) != 1 {
		t.Fatal("missing stats adapter reply")
	}
	want := demonware.TaskReply(1, 1, 0, nil)
	if frames[0].Payload != hex.EncodeToString(want) {
		t.Fatal("stats adapter no-player empty success changed")
	}
	marketplaceOutcome(t, l.onCTRStats(1, demonware.NewReader(nil)), lobby.ErrUnhandled)
}
