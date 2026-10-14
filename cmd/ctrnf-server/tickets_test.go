package main

import (
	sql "database/sql"
	fmt "fmt"
	authpkg "github.com/pierluigizagaria/demonware-server/internal/auth"
	platformpkg "github.com/pierluigizagaria/demonware-server/internal/platform"
	ps4pkg "github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	storepkg "github.com/pierluigizagaria/demonware-server/internal/store"
	filepath "path/filepath"
	testing "testing"
	time "time"
)

func issueTestTicket(ticket []byte, id platformpkg.PlayerID) {
	testTickets.RememberTicket(ticket, id, time.Now(), ps4pkg.TicketWindow)
}

func issuedTicketCount() int {
	testTickets.Mu.Lock()
	defer testTickets.Mu.Unlock()
	return len(testTickets.Entries)
}

func withTestPlayerNames(t *testing.T) *storepkg.CtrSQLite {
	t.Helper()
	db := openTestCTR(t, filepath.Join(t.TempDir(), storepkg.CtrDB), false)
	old := testPresence.PlayerNames
	testPresence.PlayerNames = db
	t.Cleanup(func() { testPresence.PlayerNames = old })
	return db
}

func TestIssuedTicketsLeaveMemoryAfterTheirWindow(t *testing.T) {
	now := time.Now()
	window := 120 * time.Second
	id := platformpkg.PlayerID{PID: 17, Username: "Fixture"}
	expired := psnTicket(now.Add(-window - authpkg.IssuedIdentityGrace))
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

func TestPlayerNamesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), storepkg.CtrDB)
	db := openTestCTR(t, path, false)
	at := time.Now().Add(-time.Hour)
	for _, r := range []struct {
		pid  uint64
		name string
		at   time.Time
	}{{9, "Later", at}, {9, "Renamed", at.Add(time.Minute)}, {10, "Later", at.Add(2 * time.Minute)}} {
		if err := db.RememberName(r.pid, r.name, r.at); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db = openTestCTR(t, path, false)
	got, err := db.NamedPlayers([]string{"LATER", "renamed", "stranger"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["later"] == nil || got["later"].PID != 10 || got["renamed"] == nil || got["renamed"].PID != 9 || got["renamed"].Username != "Renamed" {
		t.Fatalf("names after restart: %+v", got)
	}
}

func TestCTRSchemaRefusesPreReleaseVersions(t *testing.T) {
	for _, old := range []int{2, 3} {
		path := filepath.Join(t.TempDir(), storepkg.CtrDB)
		db := openTestCTR(t, path, false)
		sqliteExec(t, db, fmt.Sprintf("PRAGMA user_version = %d", old))
		if err := db.RememberName(9, "Fixture", time.Now()); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if other, err := storepkg.OpenCTRSQLite(path, false, testTrustID); err == nil {
			other.Close()
			t.Fatalf("pre-release schema %d accepted", old)
		}
		// A refused open must leave the old database recoverable.
		check, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { check.Close() })
		var version int
		if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != old {
			t.Fatalf("version %d changed to %d, %v", old, version, err)
		}
		var name string
		if err := check.QueryRow("SELECT name FROM player_names WHERE pid = 9").Scan(&name); err != nil || name != "Fixture" {
			t.Fatalf("refused database lost its name: %q, %v", name, err)
		}
	}
}

func TestCTRDatabaseRefusesAnotherTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), storepkg.CtrDB)
	openTestCTR(t, path, false).Close()
	if other, err := storepkg.OpenCTRSQLite(path, false, "another-issuer"); err == nil {
		other.Close()
		t.Fatal("database opened for another shadNet issuer")
	}
	if _, err := storepkg.OpenCTRSQLite(filepath.Join(t.TempDir(), storepkg.CtrDB), false, ""); err == nil {
		t.Fatal("database opened without a trust ID")
	}
	for _, statement := range []string{"DELETE FROM meta WHERE key = 'trust_id'", "UPDATE meta SET value = 'x' WHERE key = 'trust_id'"} {
		db := openTestCTR(t, path, false)
		sqliteExec(t, db, statement)
		db.Close()
		if reopened, err := storepkg.OpenCTRSQLite(path, false, testTrustID); err == nil {
			reopened.Close()
			t.Fatalf("%s accepted", statement)
		}
		path = filepath.Join(t.TempDir(), storepkg.CtrDB)
		openTestCTR(t, path, false).Close()
	}
}
