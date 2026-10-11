package main

import "fmt"

// CTR_UNLOCK_ALL is fixed for the instance, independently of platform or admission.
func parseUnlockAll(v string) (bool, error) {
	switch v {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		return true, nil
	default:
		return false, fmt.Errorf("invalid CTR_UNLOCK_ALL (expected 0, false, 1 or true)")
	}
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

// inventory computes unlock-all ownership without persisting catalog items.
// Save-owned unlocks (Adventure, Time Trial, cheats) stay outside server control.
func (s *economyStore) inventory(pid uint64) (*economyAccount, error) {
	if s.unlockAll {
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
