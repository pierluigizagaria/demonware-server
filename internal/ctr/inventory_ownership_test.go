package ctr

import "testing"

func TestUnlockAllInventoryDoesNotExposeCatalog(t *testing.T) {
	store := &EconomyStore{UnlockAll: true}
	first, err := store.Inventory(17)
	if err != nil || len(first.Items) == 0 {
		t.Fatal("catalog inventory missing")
	}
	count := len(first.Items)
	for id := range first.Items {
		delete(first.Items, id)
	}
	first.Items[0] = true
	second, err := store.Inventory(18)
	if err != nil || len(second.Items) != count || second.Items[0] {
		t.Fatal("inventory mutation changed the catalog")
	}
}
