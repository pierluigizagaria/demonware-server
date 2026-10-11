package main

import (
	"encoding/json"
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

func TestPruneCarriesLegacyIdentityFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write := func(key string, id playerID, at time.Time) string {
		raw, _ := json.Marshal(id)
		p := filepath.Join(dir, "id_"+key+".json")
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		age(t, at, p)
		return p
	}
	// Two logins of one account under different names, without account files.
	write("0000000000000001", playerID{PID: 42, Username: "OldName"}, now.Add(-2*time.Hour))
	write("0000000000000002", playerID{PID: 42, Username: "NewName"}, now.Add(-time.Hour))
	pruneIssuedTickets(dir, 120*time.Second, now)
	if files, _ := filepath.Glob(filepath.Join(dir, "id_*.json")); len(files) != 0 {
		t.Fatalf("legacy identity files kept: %v", files)
	}
	forgetKnownIdentities(dir)
	known := knownIdentities(dir, []string{"newname", "oldname"})
	if known["newname"] == nil || known["newname"].PID != 42 || known["oldname"] != nil {
		t.Fatalf("latest legacy identity not carried over: %+v", known)
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
