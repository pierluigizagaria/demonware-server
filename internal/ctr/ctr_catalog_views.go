package ctr

import (
	json "encoding/json"
	maps "maps"
	slices "slices"
)

// Return owned copies of the immutable reference data for diagnostics and
// compatibility checks; the package's catalog maps and slices remain private.
func EconomyCatalogSnapshot() map[uint32]economySKU {
	out := maps.Clone(economyCatalog)
	for id, sku := range out {
		sku.Items = slices.Clone(sku.Items)
		sku.Parts = slices.Clone(sku.Parts)
		out[id] = sku
	}
	return out
}

func EconomySKUItems(id uint32) []uint32 { return slices.Clone(economyCatalog[id].Items) }

func CatalogItemsSnapshot() map[uint32]bool { return maps.Clone(catalogItems) }

func RaceRatesSnapshot() map[uint64]raceRate { return maps.Clone(raceRates) }

func ChallengeScheduleSnapshot() challengeSchedule {
	var out challengeSchedule
	if err := json.Unmarshal(challengeRulesJSON, &out); err != nil {
		panic(err)
	}
	return out
}

func WindowShoppingSnapshot() windowShoppingRule {
	var out windowShoppingRule
	if err := json.Unmarshal(windowShoppingJSON, &out); err != nil {
		panic(err)
	}
	return out
}
