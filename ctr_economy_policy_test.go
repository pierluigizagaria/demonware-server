package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

func TestSQLiteUnlockAllOwnsCatalogWithoutWrites(t *testing.T) {
	s, db := newTestEconomy(t, true)
	a, err := s.inventory(42)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Items, catalogItemsSnapshot()) || len(catalogItemsSnapshot()) == 0 {
		t.Fatal("inventory differs from catalog")
	}
	for _, sku := range economyCatalogSnapshot() {
		if !ownsSKU(a, sku) {
			t.Fatal("catalog SKU not owned")
		}
	}
	if got := inventoryPage(a, 1, 4096); len(got) != len(catalogItemsSnapshot()) {
		t.Fatalf("first page = %d items", len(got))
	}
	before := sqliteChanges(t, db)
	if debit, err := s.purchase(42, []uint32{200085, 200597}); err != nil || debit != 0 {
		t.Fatalf("purchase = %d, %v", debit, err)
	}
	if _, err := s.purchase(42, []uint32{200085, 1}); err == nil {
		t.Fatal("unknown SKU accepted")
	}
	if got := sqliteChanges(t, db); got != before {
		t.Fatalf("unlock-all wrote rows: %d -> %d", before, got)
	}
	for _, table := range []string{"accounts", "balances", "inventory", "ledger"} {
		if got := sqliteCount(t, db, table); got != 0 {
			t.Fatalf("%s has %d rows", table, got)
		}
	}
}

func TestSQLiteNormalInventoryIsPersisted(t *testing.T) {
	setStartBalance(t, 10000)
	s, _ := newTestEconomy(t, false)
	a, err := s.inventory(42)
	if err != nil || len(a.Items) != 0 {
		t.Fatalf("fresh inventory = %v, %v", a, err)
	}
	if _, err := s.purchase(42, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	a, err = s.inventory(42)
	if err != nil || len(a.Items) != len(economyCatalogSnapshot()[200085].Items) || a.Balances[wumpaCurrency] != 10000-economyCatalogSnapshot()[200085].Price {
		t.Fatal("inventory does not reflect persisted purchases")
	}
}

func TestPlatformMarketplaceUnlockAllInventory(t *testing.T) {
	// Exercise the PS4 wire path using the same backend installed at startup.
	old := testCTR.economy
	t.Cleanup(func() { testCTR.economy = old })
	s, db := newTestEconomy(t, true)
	testCTR.economy = s
	setStartBalance(t, 10000)
	before := sqliteChanges(t, db)
	l, wire := sharedPS4Lobby()
	l.onTask(marketplaceHex(t, ps4PurchaseRequest))
	frames := marketplaceFrames(t, l, wire)
	if len(frames) != 2 {
		t.Fatal("purchase must reply before balance push")
	}
	marketplaceOutcome(t, frames[0][1:], 0)
	a, err := s.snapshot(l.pid())
	if err != nil || a.Balances[wumpaCurrency] != 10000 || len(a.Items) != 0 {
		t.Fatal("unlock-all purchase debited or granted persisted items")
	}
	if sqliteChanges(t, db) != before {
		t.Fatal("unlock-all purchase or balance push wrote rows")
	}
	w := demonware.NewWriter([]byte{svcMarketplace, demonware.TagUint8, taskGetInventoryPaginated})
	w.String("octane_psn")
	w.Uint32(1)
	w.Uint32(4096)
	w.Raw([]byte{0})
	reply := sharedTask(t, l, wire, w.Bytes())
	marketplaceOutcome(t, reply, 0)
	r := demonware.NewReader(reply[14:])
	task, e1 := r.Uint8()
	count, e2 := r.Uint32()
	if e1 != nil || e2 != nil || task != taskGetInventoryPaginated || int(count) != len(catalogItemsSnapshot()) {
		t.Fatalf("inventory count = %d", count)
	}
}

func TestSQLiteUsesDataDir(t *testing.T) {
	old, oldDir := testCTR.economy, testConfig.dataDir
	t.Cleanup(func() { testCTR.economy, testConfig.dataDir = old, oldDir })
	for _, ns := range []string{"", "nested"} {
		t.Run(ns, func(t *testing.T) {
			root := t.TempDir()
			testCTR.economy, testConfig.dataDir = &economyStore{initialBalance: func() int64 { return testStartBalance }}, filepath.Join(root, ns)
			testLoadWallets()
			if testCTR.economy.dir != filepath.Join(root, ns) {
				t.Fatalf("wallet dir = %s", testCTR.economy.dir)
			}
			db := openTestCTR(t, filepath.Join(testCTR.economy.dir, ctrDB), false)
			testCTR.economy.backend = db
			if _, err := testCTR.economy.snapshot(7); err != nil {
				t.Fatal(err)
			}
			if err := db.write([]ctrStatWrite{{ctrStat: ctrStat{Board: 89, PID: 7, Score: 2}}}); err != nil {
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
