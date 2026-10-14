package main

import (
	ctrpkg "github.com/NextendoNetwork/nextendo-demonware/internal/ctr"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	storepkg "github.com/NextendoNetwork/nextendo-demonware/internal/store"
	os "os"
	filepath "path/filepath"
	reflect "reflect"
	testing "testing"
)

func TestSQLiteUnlockAllOwnsCatalogWithoutWrites(t *testing.T) {
	s, db := newTestEconomy(t, true)
	a, err := s.Inventory(42)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Items, ctrpkg.CatalogItemsSnapshot()) || len(ctrpkg.CatalogItemsSnapshot()) == 0 {
		t.Fatal("inventory differs from catalog")
	}
	for _, sku := range ctrpkg.EconomyCatalogSnapshot() {
		if !ctrpkg.OwnsSKU(a, sku) {
			t.Fatal("catalog SKU not owned")
		}
	}
	if got := ctrpkg.InventoryPage(a, 1, 4096); len(got) != len(ctrpkg.CatalogItemsSnapshot()) {
		t.Fatalf("first page = %d items", len(got))
	}
	before := sqliteChanges(t, db)
	if debit, err := s.Purchase(42, []uint32{200085, 200597}); err != nil || debit != 0 {
		t.Fatalf("purchase = %d, %v", debit, err)
	}
	if _, err := s.Purchase(42, []uint32{200085, 1}); err == nil {
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
	a, err := s.Inventory(42)
	if err != nil || len(a.Items) != 0 {
		t.Fatalf("fresh inventory = %v, %v", a, err)
	}
	if _, err := s.Purchase(42, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	a, err = s.Inventory(42)
	if err != nil || len(a.Items) != len(ctrpkg.EconomyCatalogSnapshot()[200085].Items) || a.Balances[ctrpkg.WumpaCurrency] != 10000-ctrpkg.EconomyCatalogSnapshot()[200085].Price {
		t.Fatal("inventory does not reflect persisted purchases")
	}
}

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
	a, err := s.Snapshot(l.Pid())
	if err != nil || a.Balances[ctrpkg.WumpaCurrency] != 10000 || len(a.Items) != 0 {
		t.Fatal("unlock-all purchase debited or granted persisted items")
	}
	if sqliteChanges(t, db) != before {
		t.Fatal("unlock-all purchase or balance push wrote rows")
	}
	w := demonwarepkg.NewWriter([]byte{ctrpkg.SvcMarketplace, demonwarepkg.TagUint8, ctrpkg.TaskGetInventoryPaginated})
	w.String("octane_psn")
	w.Uint32(1)
	w.Uint32(4096)
	w.Raw([]byte{0})
	reply := sharedTask(t, l, wire, w.Bytes())
	marketplaceOutcome(t, reply, 0)
	r := demonwarepkg.NewReader(reply[14:])
	task, e1 := r.Uint8()
	count, e2 := r.Uint32()
	if e1 != nil || e2 != nil || task != ctrpkg.TaskGetInventoryPaginated || int(count) != len(ctrpkg.CatalogItemsSnapshot()) {
		t.Fatalf("inventory count = %d", count)
	}
}

func TestSQLiteUsesDataDir(t *testing.T) {
	old, oldDir := testCTR.Economy, testConfig.DataDir
	t.Cleanup(func() { testCTR.Economy, testConfig.DataDir = old, oldDir })
	for _, ns := range []string{"", "nested"} {
		t.Run(ns, func(t *testing.T) {
			root := t.TempDir()
			testCTR.Economy, testConfig.DataDir = &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }}, filepath.Join(root, ns)
			testLoadWallets()
			if testCTR.Economy.Dir != filepath.Join(root, ns) {
				t.Fatalf("wallet dir = %s", testCTR.Economy.Dir)
			}
			db := openTestCTR(t, filepath.Join(testCTR.Economy.Dir, storepkg.CtrDB), false)
			testCTR.Economy.Backend = db
			if _, err := testCTR.Economy.Snapshot(7); err != nil {
				t.Fatal(err)
			}
			if err := db.Write([]ctrpkg.CtrStatWrite{{CtrStat: ctrpkg.CtrStat{Board: 89, PID: 7, Score: 2}}}); err != nil {
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
