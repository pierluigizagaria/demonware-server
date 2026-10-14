package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

func TestPlatformMarketplaceUnlockAllInventory(t *testing.T) {
	// Exercise the PS4 wire path using the same backend installed at startup.
	old := testCTR.Economy
	t.Cleanup(func() { testCTR.Economy = old })
	s, db := newTestEconomy(t, true)
	testCTR.Economy = s
	setStartBalance(t, 10000)
	before := sqliteChanges(t, db)
	l, wire := sharedPS4Lobby()
	l.onTask(marketplaceHex(t, ps4PurchaseRequest))
	frames := marketplaceFrames(t, l, wire)
	if len(frames) != 2 {
		t.Fatal("purchase must reply before balance push")
	}
	marketplaceOutcome(t, frames[0][1:], 0)
	a, err := s.Snapshot(l.PID())
	if err != nil || a.Balances[ctr.WumpaCurrency] != 10000 || len(a.Items) != 0 {
		t.Fatal("unlock-all purchase debited or granted persisted items")
	}
	if sqliteChanges(t, db) != before {
		t.Fatal("unlock-all purchase or balance push wrote rows")
	}
	w := demonware.NewWriter([]byte{ctr.SvcMarketplace, demonware.TagUint8, ctr.TaskGetInventoryPaginated})
	w.String("octane_psn")
	w.Uint32(1)
	w.Uint32(4096)
	w.Raw([]byte{0})
	reply := sharedTask(t, l, wire, w.Bytes())
	marketplaceOutcome(t, reply, 0)
	r := demonware.NewReader(reply[14:])
	task, e1 := r.Uint8()
	count, e2 := r.Uint32()
	if e1 != nil || e2 != nil || task != ctr.TaskGetInventoryPaginated || int(count) != len(ctr.CatalogItemsSnapshot()) {
		t.Fatalf("inventory count = %d", count)
	}
}

func TestSQLiteUsesDataDir(t *testing.T) {
	old, oldDir := testCTR.Economy, testConfig.DataDir
	t.Cleanup(func() { testCTR.Economy, testConfig.DataDir = old, oldDir })
	for _, ns := range []string{"", "nested"} {
		t.Run(ns, func(t *testing.T) {
			root := t.TempDir()
			testCTR.Economy, testConfig.DataDir = &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }}, filepath.Join(root, ns)
			testLoadWallets()
			if testCTR.Economy.Dir != filepath.Join(root, ns) {
				t.Fatalf("wallet dir = %s", testCTR.Economy.Dir)
			}
			db := openTestCTR(t, filepath.Join(testCTR.Economy.Dir, store.DBFile), false)
			testCTR.Economy.Backend = db
			if _, err := testCTR.Economy.Snapshot(7); err != nil {
				t.Fatal(err)
			}
			if err := db.Write([]ctr.StatWrite{{Stat: ctr.Stat{Board: 89, PID: 7, Score: 2}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, ns, "ctrnf.db")); err != nil {
				t.Fatal(err)
			}
			if sqliteCount(t, db, "accounts") != 1 || sqliteCount(t, db, "leaderboard") != 1 {
				t.Fatal("economy and leaderboard did not share the instance database")
			}
		})
	}
}
