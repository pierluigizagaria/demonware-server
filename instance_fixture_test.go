package main

import (
	"net/http"
	"testing"
	"time"
)

// Legacy fixtures share this explicit test owner. Production has no fallback
// ticket directory; auth, lobby and Umbrella receive the same instance owner.
var testTickets = newTicketStore()

func testAuthHandler(p publisherPlatform, s *ctrAuthSigner, dumps string) http.Handler {
	return newPlatformAuthHandler(testTickets, p, s, dumps)
}

func testUmbrellaHandler(w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
	handleCTRUmbrella(testTickets, w, r, body, n, dumps)
}

func TestInstanceTicketDirectoriesAreIsolated(t *testing.T) {
	a, b := newTicketStore(), newTicketStore()
	now := time.Now()
	ticket := psnTicket(now)
	a.rememberTicket(ticket, playerID{PID: 17, Username: "first", Title: 5760}, now, ticketWindow)
	if _, ok := b.issuedTicketPlayer(ticket, now, ticketWindow); ok {
		t.Fatal("another instance admitted a foreign ticket")
	}
	b.rememberTicket(ticket, playerID{PID: 17, Username: "second", Title: 5760}, now, ticketWindow)
	first, okA := a.issuedIdentity(ticket)
	second, okB := b.issuedIdentity(ticket)
	if !okA || !okB || first.Username != "first" || second.Username != "second" {
		t.Fatal("same numeric identity crossed instance directories")
	}
}
