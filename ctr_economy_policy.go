package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// economyPolicy is fixed for the whole server instance at startup. It never
// depends on the player, the platform or how the player was admitted, so two
// differently configured servers must run as two instances with their own
// CTR_WALLETS directory.
type economyPolicy int

const (
	// economyAuthentic keeps the ledger: balances, rewards and purchases.
	economyAuthentic economyPolicy = iota
	// economyUnlockAll reports every catalog item as owned. Ownership is
	// computed at read time and never written, and purchases debit nothing.
	// Save-owned unlocks (Adventure, Time Trial, cheats) are out of reach.
	economyUnlockAll
)

func (p economyPolicy) String() string {
	if p == economyUnlockAll {
		return "unlock-all"
	}
	return "authentic"
}

// parseEconomyPolicy reads CTR_ECONOMY; unset keeps the original economy.
func parseEconomyPolicy(v string) (economyPolicy, error) {
	switch v {
	case "", "authentic":
		return economyAuthentic, nil
	case "unlock-all":
		return economyUnlockAll, nil
	}
	return 0, fmt.Errorf("unknown CTR_ECONOMY %q (authentic or unlock-all)", v)
}

// Every item any catalog SKU grants. Read-only after initialization.
var catalogItems = func() map[uint32]bool {
	m := map[uint32]bool{}
	for _, sku := range economyCatalog {
		for _, item := range sku.Items {
			m[item] = true
		}
	}
	return m
}()

const economyPolicyMarker = "economy-policy"

// bindPolicy records the policy in the wallet directory on first use and
// refuses a directory recorded for the other policy, so race rewards earned on
// an unlock-all server never reach an authentic ledger, or the reverse.
func (s *economyStore) bindPolicy(p economyPolicy) error {
	path := filepath.Join(s.dir, economyPolicyMarker)
	want := []byte(p.String() + "\n")
	got, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, want, 0o600); err != nil {
			return err
		}
		s.policy = p
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("wallet directory belongs to the %s economy, not %s", strings.TrimSpace(string(got)), p)
	}
	s.policy = p
	return nil
}

// inventory returns the account view used for Pit Stop inventory replies.
func (s *economyStore) inventory(pid uint64) (*economyAccount, error) {
	if s.policy == economyUnlockAll {
		return &economyAccount{Version: 2, Balances: map[uint64]int64{}, Items: catalogItems}, nil
	}
	return s.snapshot(pid)
}

// purchaseUnlockAll accepts only known SKUs, debits nothing and writes nothing.
func purchaseUnlockAll(skus []uint32) (int64, error) {
	for _, id := range skus {
		if _, ok := economyCatalog[id]; !ok {
			return 0, fmt.Errorf("unknown SKU %d", id)
		}
	}
	return 0, nil
}

// unscopedWalletFiles counts ledger files left in the CTR_WALLETS root by
// builds that did not namespace PS4 accounts. They are never read or moved
// automatically: a shadNet account ID is meaningful only for its issuer.
func unscopedWalletFiles(root string) int {
	n := 0
	for _, pattern := range []string{"*.economy-v2.json", "ctr-leaderboards-v1.json"} {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		n += len(matches)
	}
	return n
}
