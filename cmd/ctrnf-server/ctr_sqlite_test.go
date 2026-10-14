package main

import (
	"encoding/json"
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

func sqliteExec(t *testing.T, db *store.SQLite, query string, args ...any) {
	t.Helper()
	if _, err := db.DB.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func accountJSON(t *testing.T, s *ctr.EconomyStore, pid uint64) []byte {
	t.Helper()
	a, err := s.Snapshot(pid)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type economyResult struct {
	credit int64
	update []byte
	failed bool
}
