package main

import (
	"encoding/json"
	"maps"
	"slices"
)

// Return owned copies of the immutable reference data for diagnostics and
// compatibility checks; the package's catalog maps and slices remain private.
func economyCatalogSnapshot() map[uint32]economySKU {
	out := maps.Clone(economyCatalog)
	for id, sku := range out {
		sku.Items = slices.Clone(sku.Items)
		sku.Parts = slices.Clone(sku.Parts)
		out[id] = sku
	}
	return out
}
func economySKUItems(id uint32) []uint32     { return slices.Clone(economyCatalog[id].Items) }
func catalogItemsSnapshot() map[uint32]bool  { return maps.Clone(catalogItems) }
func raceRatesSnapshot() map[uint64]raceRate { return maps.Clone(raceRates) }
func challengeScheduleSnapshot() challengeSchedule {
	var out challengeSchedule
	if err := json.Unmarshal(challengeRulesJSON, &out); err != nil {
		panic(err)
	}
	return out
}
func windowShoppingSnapshot() windowShoppingRule {
	var out windowShoppingRule
	if err := json.Unmarshal(windowShoppingJSON, &out); err != nil {
		panic(err)
	}
	return out
}
