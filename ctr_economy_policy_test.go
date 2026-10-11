package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEconomyPolicyParse(t *testing.T) {
	for in, want := range map[string]economyPolicy{"": economyAuthentic, "authentic": economyAuthentic, "unlock-all": economyUnlockAll} {
		if got, err := parseEconomyPolicy(in); err != nil || got != want {
			t.Fatalf("%q = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"unlock_all", "Authentic", "all", "1"} {
		if _, err := parseEconomyPolicy(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

func TestEconomyPolicyBindsWalletDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := (&economyStore{dir: dir}).bindPolicy(economyUnlockAll); err != nil {
		t.Fatal(err)
	}
	s := &economyStore{dir: dir}
	if err := s.bindPolicy(economyUnlockAll); err != nil || s.policy != economyUnlockAll {
		t.Fatalf("same policy refused: %v", err)
	}
	other := &economyStore{dir: dir}
	if err := other.bindPolicy(economyAuthentic); err == nil {
		t.Fatal("authentic instance accepted an unlock-all wallet directory")
	}
	if err := os.WriteFile(filepath.Join(dir, economyPolicyMarker), []byte("authentic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&economyStore{dir: dir}).bindPolicy(economyUnlockAll); err == nil {
		t.Fatal("unlock-all instance accepted an authentic wallet directory")
	}
}

func TestEconomyUnlockAllOwnsCatalogWithoutWrites(t *testing.T) {
	s := &economyStore{dir: t.TempDir(), policy: economyUnlockAll}
	a, err := s.inventory(42)
	if err != nil || len(a.Items) != len(catalogItems) || len(catalogItems) == 0 {
		t.Fatalf("inventory = %d items, %v", len(a.Items), err)
	}
	for _, sku := range economyCatalog {
		if !ownsSKU(a, sku) {
			t.Fatal("catalog SKU not owned")
		}
	}
	if got := inventoryPage(a, 1, 4096); len(got) != len(catalogItems) {
		t.Fatalf("first page = %d items", len(got))
	}
	debit, err := s.purchase(42, []uint32{200085, 200597})
	if err != nil || debit != 0 {
		t.Fatalf("purchase = %d, %v", debit, err)
	}
	if _, err := s.purchase(42, []uint32{1}); err == nil {
		t.Fatal("unknown SKU accepted")
	}
	if entries, err := os.ReadDir(s.dir); err != nil || len(entries) != 0 {
		t.Fatalf("unlock-all wrote ledger files: %v, %v", entries, err)
	}
}

func TestEconomyAuthenticInventoryIsLedger(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "10000")
	s := &economyStore{dir: t.TempDir()}
	if a, err := s.inventory(42); err != nil || len(a.Items) != 0 {
		t.Fatalf("fresh authentic inventory = %v, %v", a, err)
	}
	if _, err := s.purchase(42, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	a, err := s.inventory(42)
	if err != nil || len(a.Items) != len(economyCatalog[200085].Items) || a.Balances[wumpaCurrency] != 10000-economyCatalog[200085].Price {
		t.Fatal("authentic inventory does not reflect the ledger")
	}
}

func TestPlatformMarketplaceUnlockAllInventory(t *testing.T) {
	s := marketplaceTestStore(t)
	s.policy = economyUnlockAll
	l, wire := sharedPS4Lobby()
	l.onTask(marketplaceHex(t, ps4PurchaseRequest))
	frames := marketplaceFrames(t, l, wire)
	if len(frames) != 2 {
		t.Fatal("purchase must reply before balance push")
	}
	marketplaceOutcome(t, frames[0][1:], 0)
	a, err := (&economyStore{dir: s.dir}).snapshot(l.pid())
	if err != nil || a.Balances[wumpaCurrency] != 10000 || len(a.Items) != 0 {
		t.Fatal("unlock-all purchase debited or granted ledger items")
	}
	w := &bdWriter{b: []byte{svcMarketplace, tagU8, taskGetInventoryPaginated}}
	w.strv("octane_psn")
	w.u32(1)
	w.u32(4096)
	w.b = append(w.b, 0)
	reply := sharedTask(t, l, wire, w.b)
	marketplaceOutcome(t, reply, 0)
	r := &bdReader{b: reply, off: 14}
	task, e1 := r.u8()
	count, e2 := r.u32()
	if e1 != nil || e2 != nil || task != taskGetInventoryPaginated || int(count) != len(catalogItems) {
		t.Fatalf("inventory count = %d", count)
	}
}

func TestWalletsUseIssuerNamespace(t *testing.T) {
	old, oldNS := economy, walletNamespace
	t.Cleanup(func() { economy, walletNamespace = old, oldNS })
	root := t.TempDir()
	t.Setenv("CTR_WALLETS", root)
	if err := os.WriteFile(filepath.Join(root, "7.economy-v2.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	economy, walletNamespace = &economyStore{}, "npauth-test"
	loadWallets()
	if economy.dir != filepath.Join(root, "npauth-test") {
		t.Fatalf("wallet dir = %s", economy.dir)
	}
	if (&ctrStatsStore{}).path() != filepath.Join(root, "npauth-test", "ctr-leaderboards-v1.json") {
		t.Fatal("leaderboards are not issuer-scoped")
	}
	if n := unscopedWalletFiles(root); n != 1 {
		t.Fatalf("unscoped files = %d", n)
	}
	if n := unscopedWalletFiles(economy.dir); n != 0 {
		t.Fatalf("namespaced files = %d", n)
	}
}
