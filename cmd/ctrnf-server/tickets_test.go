package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

func issueTestTicket(ticket []byte, id platform.PlayerID) {
	testTickets.RememberTicket(ticket, id, time.Now(), ps4.TicketWindow)
}

func withTestPlayerNames(t *testing.T) *store.SQLite {
	t.Helper()
	db := openTestCTR(t, filepath.Join(t.TempDir(), store.DBFile), false)
	old := testPresence
	testPresence = presence.NewStore(db)
	t.Cleanup(func() { testPresence = old })
	return db
}
