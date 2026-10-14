package auth

import (
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func TestInstanceTicketDirectoriesAreIsolated(t *testing.T) {
	a, b := NewTicketStore(), NewTicketStore()
	now := time.Now()
	ticket := psnTicket(now)
	a.RememberTicket(ticket, platform.PlayerID{PID: 17, Username: "first", Title: 5760}, now, ps4.TicketWindow)
	if _, ok := b.IssuedTicketPlayer(ticket, now, ps4.TicketWindow); ok {
		t.Fatal("another instance admitted a foreign ticket")
	}
	b.RememberTicket(ticket, platform.PlayerID{PID: 17, Username: "second", Title: 5760}, now, ps4.TicketWindow)
	first, okA := a.IssuedIdentity(ticket)
	second, okB := b.IssuedIdentity(ticket)
	if !okA || !okB || first.Username != "first" || second.Username != "second" {
		t.Fatal("same numeric identity crossed instance directories")
	}
}
