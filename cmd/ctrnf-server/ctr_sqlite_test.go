package main

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

const testTrustID = "test-trust"

func openTestCTR(t *testing.T, path string, unlockAll bool) *store.SQLite {
	t.Helper()
	db, err := store.OpenSQLite(path, unlockAll, testTrustID)
	if err != nil {
		t.Fatal(err)
	}
	registerDatabaseInspector(t, db, path)
	db.InitialBalance = func() int64 { return testStartBalance }
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestEconomy(t *testing.T, unlockAll bool) (*ctr.EconomyStore, *store.SQLite) {
	t.Helper()
	dir := t.TempDir()
	db := openTestCTR(t, filepath.Join(dir, store.DBFile), unlockAll)
	return &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: dir, Backend: db, UnlockAll: unlockAll}, db
}

func sqliteCount(t *testing.T, db *store.SQLite, table string) int {
	t.Helper()
	var n int
	if err := inspectDatabase(t, db).QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sqliteChanges(t *testing.T, db *store.SQLite) int {
	t.Helper()
	activity, err := db.Activity()
	if err != nil {
		t.Fatal(err)
	}
	return activity.TotalChanges
}

// Composed tests independently inspect the persisted file through a read-only
// connection. They never borrow the application's live SQL pool.
var databaseInspectors = map[*store.SQLite]*sql.DB{}

func registerDatabaseInspector(t *testing.T, database *store.SQLite, path string) {
	t.Helper()
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath}).String() + "?mode=ro"
	observer, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	databaseInspectors[database] = observer
	t.Cleanup(func() { observer.Close(); delete(databaseInspectors, database) })
}

func inspectDatabase(t *testing.T, database *store.SQLite) *sql.DB {
	t.Helper()
	observer := databaseInspectors[database]
	if observer == nil {
		t.Fatal("missing read-only database fixture")
	}
	return observer
}
