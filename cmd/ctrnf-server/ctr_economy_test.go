package main

import (
	json "encoding/json"
	ctrpkg "github.com/NextendoNetwork/nextendo-demonware/internal/ctr"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	os "os"
	filepath "path/filepath"
	sync "sync"
	testing "testing"
)

func TestEconomyPurchaseRestartAndRetry(t *testing.T) {
	setStartBalance(t, 10000)
	s := &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	sku := ctrpkg.EconomyCatalogSnapshot()[200085]
	debit, err := s.Purchase(42, []uint32{200085})
	if err != nil || debit != sku.Price {
		t.Fatalf("purchase %d %v", debit, err)
	}
	s = &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: s.Dir}
	a, err := s.Snapshot(42)
	if err != nil {
		t.Fatal(err)
	}
	if a.Balances[10] != 10000-sku.Price || !ctrpkg.OwnsSKU(a, sku) {
		t.Fatalf("restart lost transaction: %+v", a)
	}
	debit, err = s.Purchase(42, []uint32{200085})
	if err != nil || debit != 0 {
		t.Fatalf("retry double charged: %d %v", debit, err)
	}
}

func TestEconomyLegacyMigrationPreservesBalanceAndOwnership(t *testing.T) {
	s := &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	os.WriteFile(filepath.Join(s.Dir, "42.json"), []byte(`{"10":9876}`), 0600)
	os.WriteFile(filepath.Join(s.Dir, "42.items.json"), []byte(`[200085]`), 0600)
	a, err := s.Snapshot(42)
	if err != nil {
		t.Fatal(err)
	}
	if a.Balances[10] != 9876 || !ctrpkg.OwnsSKU(a, ctrpkg.EconomyCatalogSnapshot()[200085]) {
		t.Fatal("legacy data lost")
	}
	if a.Items[200085] {
		t.Fatal("SKU incorrectly used as item")
	}
}

func TestEconomyNoPartialPurchase(t *testing.T) {
	setStartBalance(t, 10000)
	s := &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	if _, err := s.Purchase(42, []uint32{200085, 4294967295}); err == nil {
		t.Fatal("unknown SKU accepted")
	}
	a, _ := s.Snapshot(42)
	if a.Balances[10] != 10000 || len(a.Items) != 0 {
		t.Fatal("partial transaction persisted")
	}
}

func TestEconomyConcurrentRetry(t *testing.T) {
	setStartBalance(t, 10000)
	s := &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Purchase(42, []uint32{200085}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	a, _ := s.Snapshot(42)
	if a.Balances[10] != 10000-ctrpkg.EconomyCatalogSnapshot()[200085].Price {
		t.Fatal("concurrent debit repeated")
	}
}

func TestEconomyCorruptWalletFailsClosed(t *testing.T) {
	s := &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	os.WriteFile(filepath.Join(s.Dir, "42.json"), []byte(`broken`), 0600)
	if _, err := s.Purchase(42, []uint32{200085}); err == nil {
		t.Fatal("corrupt wallet reset")
	}
}

func TestEconomyWriteFailureDoesNotGrant(t *testing.T) {
	setStartBalance(t, 10000)
	s := &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	a, _ := s.Snapshot(42)
	// A directory at the final path forces rename to fail on Windows and Linux.
	os.Remove(s.AccountPath(42))
	os.Mkdir(s.AccountPath(42), 0700)
	if err := s.Save(42, a); err == nil {
		t.Fatal("failed commit reported success")
	}
}

func TestEconomyBundleOnlyChargesUnownedComponents(t *testing.T) {
	a := &ctrpkg.EconomyAccount{Items: map[uint32]bool{}}
	for _, sku := range ctrpkg.EconomyCatalogSnapshot() {
		if len(sku.Parts) < 2 {
			continue
		}
		first := ctrpkg.EconomyCatalogSnapshot()[sku.Parts[0]]
		for _, i := range first.Items {
			a.Items[i] = true
		}
		var expected int64
		for _, id := range sku.Parts {
			part := ctrpkg.EconomyCatalogSnapshot()[id]
			if !ctrpkg.OwnsSKU(a, part) {
				expected += part.Price
			}
		}
		price, err := ctrpkg.SkuPrice(a, sku)
		if err != nil || price != expected {
			t.Fatalf("bundle %d %v", price, err)
		}
		return
	}
	t.Fatal("no bundle fixture")
}

func TestInventoryOwnerAndCatalogItem(t *testing.T) {
	item := ctrpkg.EconomyCatalogSnapshot()[200085].Items[0]
	w := &demonwarepkg.Writer{}
	ctrpkg.WriteInventoryItem(w, 42, item)
	r := demonwarepkg.NewReader(w.Bytes())
	owner, _ := r.Uint64()
	kind, _ := r.String()
	id, _ := r.Uint32()
	if owner != 42 || kind != ps4pkg.MarketplaceAccountType || id != item {
		t.Fatalf("bad inventory owner/item %d %s %d", owner, kind, id)
	}
}

func TestInventoryPagesAreOneBased(t *testing.T) {
	a := &ctrpkg.EconomyAccount{Items: map[uint32]bool{100: true, 101: true, 102: true}}
	first := ctrpkg.InventoryPage(a, 1, 2)
	second := ctrpkg.InventoryPage(a, 2, 2)
	if len(first) != 2 || first[0] != 100 || len(second) != 1 || second[0] != 102 {
		t.Fatalf("bad pages %v %v", first, second)
	}
}

func TestEconomyInsufficientFunds(t *testing.T) {
	setStartBalance(t, 0)
	s := &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	if _, err := s.Purchase(42, []uint32{200085}); err == nil {
		t.Fatal("overspend accepted")
	}
	a, _ := s.Snapshot(42)
	b, _ := json.Marshal(a)
	if len(a.Items) != 0 || a.Balances[10] != 0 {
		t.Fatalf("overspend changed account %s", b)
	}
}
