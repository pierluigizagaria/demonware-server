package auth

import (
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

func TestIssuedTicketsLeaveMemoryAfterTheirWindow(t *testing.T) {
	testTickets := NewTicketStore()
	now := time.Now()
	window := 120 * time.Second
	id := platform.PlayerID{PID: 17, Username: "Fixture"}
	expired := psnTicket(now.Add(-window - IssuedIdentityGrace))
	closing := psnTicket(now.Add(-window))
	fresh := psnTicket(now)
	testTickets.RememberTicket(expired, id, now, window)
	testTickets.RememberTicket(closing, id, now, window)
	testTickets.RememberTicket(fresh, id, now, window)
	if _, ok := testTickets.IssuedIdentity(expired); ok {
		t.Fatal("ticket past its window and grace kept in memory")
	}
	if _, ok := testTickets.IssuedTicketPlayer(closing, now, window); ok {
		t.Fatal("ticket admitted after its window")
	}
	if got, ok := testTickets.IssuedIdentity(closing); !ok || got.PID != 17 {
		t.Fatal("identity of a just-admitted ticket lost before its grace ended")
	}
	if got, ok := testTickets.IssuedTicketPlayer(fresh, now, window); !ok || got.PID != 17 || got.Title != 5760 {
		t.Fatal("fresh ticket not admitted with its identity")
	}
	altered := append([]byte(nil), fresh...)
	altered[100] ^= 1
	if _, ok := testTickets.IssuedTicketPlayer(altered, now, window); ok {
		t.Fatal("ticket with the same tag but other bytes admitted")
	}
}
