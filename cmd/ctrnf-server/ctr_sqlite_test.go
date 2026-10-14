package main

import (
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
	if err := db.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sqliteChanges(t *testing.T, db *store.SQLite) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRow("SELECT total_changes()").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
