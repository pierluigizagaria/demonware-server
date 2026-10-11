package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func forgetKnownIdentities(dir string) {
	knownMu.Lock()
	delete(knownDirs, dir)
	knownMu.Unlock()
}

func age(t *testing.T, at time.Time, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPruneIssuedTicketsKeepsAccountsAndFreshTickets(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	window := 120 * time.Second
	old, fresh := psnTicket(now.Add(-time.Hour)), psnTicket(now)
	if err := persistTicket(dir, old, playerID{PID: 17, Username: "Fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := persistTicket(dir, fresh, playerID{PID: 17, Username: "Fixture"}); err != nil {
		t.Fatal(err)
	}
	oldFiles := []string{filepath.Join(dir, ticketKey(old)+".tkt"), filepath.Join(dir, "id_"+ticketKey(old)+".json")}
	age(t, now.Add(-time.Hour), oldFiles...)

	pruneIssuedTickets(dir, window, now)
	for _, p := range oldFiles {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expired %s kept", filepath.Base(p))
		}
	}
	if _, ok := issuedTicketPlayer(dir, fresh, now, window); !ok {
		t.Fatal("fresh ticket pruned")
	}
	forgetKnownIdentities(dir) // restart
	if got := knownIdentities(dir, []string{"fixture"})["fixture"]; got == nil || got.PID != 17 {
		t.Fatal("authenticated player no longer resolves after pruning")
	}
}

func TestKnownIdentitiesFollowIssuanceWithoutRescan(t *testing.T) {
	dir := t.TempDir()
	if got := knownIdentities(dir, []string{"later"}); len(got) != 0 {
		t.Fatal("unexpected identity")
	}
	if err := persistTicket(dir, psnTicket(time.Now()), playerID{PID: 9, Username: "Later"}); err != nil {
		t.Fatal(err)
	}
	if got := knownIdentities(dir, []string{"LATER"})["later"]; got == nil || got.PID != 9 {
		t.Fatal("issued identity missing from the loaded index")
	}
}
