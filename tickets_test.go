package main

import (
	"path/filepath"
	"testing"
	"time"
)

func issueTestTicket(ticket []byte, id playerID) {
	rememberTicket(ticket, id, time.Now(), ticketWindow)
}

func issuedTicketCount() int {
	issuedMu.Lock()
	defer issuedMu.Unlock()
	return len(issuedTickets)
}

func withTestPlayerNames(t *testing.T) *ctrSQLite {
	t.Helper()
	db := openTestCTR(t, filepath.Join(t.TempDir(), ctrDB), false)
	old := playerNames
	playerNames = db
	t.Cleanup(func() { playerNames = old })
	return db
}

func TestIssuedTicketsLeaveMemoryAfterTheirWindow(t *testing.T) {
	now := time.Now()
	window := 120 * time.Second
	id := playerID{PID: 17, Username: "Fixture"}
	expired := psnTicket(now.Add(-window - issuedIdentityGrace))
	closing := psnTicket(now.Add(-window))
	fresh := psnTicket(now)
	rememberTicket(expired, id, now, window)
	rememberTicket(closing, id, now, window)
	rememberTicket(fresh, id, now, window)
	if _, ok := issuedIdentity(expired); ok {
		t.Fatal("ticket past its window and grace kept in memory")
	}
	if _, ok := issuedTicketPlayer(closing, now, window); ok {
		t.Fatal("ticket admitted after its window")
	}
	if got, ok := issuedIdentity(closing); !ok || got.PID != 17 {
		t.Fatal("identity of a just-admitted ticket lost before its grace ended")
	}
	if got, ok := issuedTicketPlayer(fresh, now, window); !ok || got.PID != 17 || got.Title != 5760 {
		t.Fatal("fresh ticket not admitted with its identity")
	}
	altered := append([]byte(nil), fresh...)
	altered[100] ^= 1
	if _, ok := issuedTicketPlayer(altered, now, window); ok {
		t.Fatal("ticket with the same tag but other bytes admitted")
	}
}

func TestPlayerNamesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), ctrDB)
	db := openTestCTR(t, path, false)
	at := time.Now().Add(-time.Hour)
	for _, r := range []struct {
		pid  uint64
		name string
		at   time.Time
	}{{9, "Later", at}, {9, "Renamed", at.Add(time.Minute)}, {10, "Later", at.Add(2 * time.Minute)}} {
		if err := db.rememberName(r.pid, r.name, r.at); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db = openTestCTR(t, path, false)
	got, err := db.namedPlayers([]string{"LATER", "renamed", "stranger"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["later"] == nil || got["later"].PID != 10 || got["renamed"] == nil || got["renamed"].PID != 9 || got["renamed"].Username != "Renamed" {
		t.Fatalf("names after restart: %+v", got)
	}
}

func TestCTRSchemaOneGainsPlayerNamesAndTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), ctrDB)
	db := openTestCTR(t, path, false)
	sqliteExec(t, db, "DROP TABLE player_names")
	sqliteExec(t, db, "DELETE FROM meta WHERE key = 'trust_id'")
	sqliteExec(t, db, "PRAGMA user_version = 1")
	db.Close()
	if other, err := openCTRSQLite(path, true, testTrustID); err == nil {
		other.Close()
		t.Fatal("mode mismatch accepted during the upgrade")
	}
	db = openTestCTR(t, path, false)
	var version int
	if err := db.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != ctrSchemaVersion {
		t.Fatalf("version %d, %v", version, err)
	}
	if err := db.rememberName(9, "Fixture", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCTRDatabaseRefusesAnotherTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), ctrDB)
	openTestCTR(t, path, false).Close()
	if other, err := openCTRSQLite(path, false, "another-issuer"); err == nil {
		other.Close()
		t.Fatal("database opened for another shadNet issuer")
	}
	if _, err := openCTRSQLite(filepath.Join(t.TempDir(), ctrDB), false, ""); err == nil {
		t.Fatal("database opened without a trust ID")
	}
	for _, statement := range []string{"DELETE FROM meta WHERE key = 'trust_id'", "UPDATE meta SET value = 'x' WHERE key = 'trust_id'"} {
		db := openTestCTR(t, path, false)
		sqliteExec(t, db, statement)
		db.Close()
		if reopened, err := openCTRSQLite(path, false, testTrustID); err == nil {
			reopened.Close()
			t.Fatalf("%s accepted", statement)
		}
		path = filepath.Join(t.TempDir(), ctrDB)
		openTestCTR(t, path, false).Close()
	}
}
