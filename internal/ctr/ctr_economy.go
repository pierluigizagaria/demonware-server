package ctr

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/pierluigizagaria/demonware-server/internal/config"
)

// Currency IDs are assigned from CMarketplaceData's baseCurrencyId (10).
const WumpaCurrency = uint64(10)

//go:embed ctr-skus.json
var catalogJSON []byte

type economySKU struct {
	Items []uint32 `json:"items"`
	Parts []uint32 `json:"parts"`
	Price int64    `json:"full_price"`
}

var economyCatalog = func() map[uint32]economySKU {
	m := map[uint32]economySKU{}
	if err := json.Unmarshal(catalogJSON, &m); err != nil {
		panic(err)
	}
	return m
}()

// One atomic file contains both the debit and item grant. Legacy files remain
// untouched for rollback; after migration only this versioned file is read.
type EconomyAccount struct {
	Challenges     map[string]ChallengeCompletion `json:"challenges,omitempty"`
	PitStopRefresh *PitStopRefreshRecord          `json:"pitstop_refresh,omitempty"`
	Version        int                            `json:"version"`
	Balances       map[uint64]int64               `json:"balances"`
	Items          map[uint32]bool                `json:"items"`
	RaceReceipts   map[string]bool                `json:"race_receipts,omitempty"`
	OnlineSeconds  map[string]uint64              `json:"online_seconds,omitempty"`
}

type EconomyStore struct {
	InitialBalance func() int64
	mu             sync.Mutex
	Dir            string
	UnlockAll      bool
	Backend        economyBackend
}

type CtrServices struct {
	Economy *EconomyStore
	Stats   LeaderboardStore
	News    config.CtrNewsConfig
}

func readEconomyJSON(path string, value any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func (s *EconomyStore) AccountPath(pid uint64) string {
	return filepath.Join(s.Dir, strconv.FormatUint(pid, 10)+".economy-v2.json")
}

func (s *EconomyStore) Load(pid uint64) (*EconomyAccount, error) {
	if s.Backend != nil {
		return s.Backend.Load(pid, false)
	}
	a := &EconomyAccount{}
	err := readEconomyJSON(s.AccountPath(pid), a)
	if err == nil {
		if a.Version != 2 || a.Balances == nil || a.Items == nil {
			return nil, fmt.Errorf("invalid economy account")
		}
		return a, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	a = &EconomyAccount{Version: 2, Balances: map[uint64]int64{}, Items: map[uint32]bool{}}
	base := filepath.Join(s.Dir, strconv.FormatUint(pid, 10))
	if err := readEconomyJSON(base+".json", &a.Balances); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if a.Balances == nil {
		return nil, fmt.Errorf("null legacy wallet")
	}
	if _, ok := a.Balances[WumpaCurrency]; !ok {
		a.Balances[WumpaCurrency] = s.startingBalance()
	}
	var skus []uint32
	if err := readEconomyJSON(base+".items.json", &skus); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, id := range skus {
		entry, ok := economyCatalog[id]
		if !ok {
			return nil, fmt.Errorf("unknown legacy SKU %d", id)
		}
		for _, item := range entry.Items {
			a.Items[item] = true
		}
	}
	if err := s.Save(pid, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *EconomyStore) Save(pid uint64, a *EconomyAccount) error {
	return s.SaveChange(pid, a, EconomyChange{Source: "account update"})
}

func (s *EconomyStore) SaveChange(pid uint64, a *EconomyAccount, change EconomyChange) error {
	if s.Backend != nil {
		return s.Backend.Save(pid, a, change)
	}
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".economy-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, s.AccountPath(pid))
}

func (s *EconomyStore) Snapshot(pid uint64) (*EconomyAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Backend != nil {
		return s.Backend.Load(pid, !s.UnlockAll)
	}
	return s.Load(pid)
}

func OwnsSKU(a *EconomyAccount, sku economySKU) bool {
	if len(sku.Items) == 0 {
		return false
	}
	for _, id := range sku.Items {
		if !a.Items[id] {
			return false
		}
	}
	return true
}

func SkuPrice(a *EconomyAccount, sku economySKU) (int64, error) {
	if OwnsSKU(a, sku) {
		return 0, nil
	}
	if len(sku.Parts) == 0 {
		return sku.Price, nil
	}
	var price int64
	// CMarketplaceBundleSku::getDiscountPrice sums unowned component prices.
	for _, id := range sku.Parts {
		part, ok := economyCatalog[id]
		if !ok {
			return 0, fmt.Errorf("missing component %d", id)
		}
		if !OwnsSKU(a, part) {
			price += part.Price
		}
	}
	return price, nil
}

func (s *EconomyStore) Purchase(pid uint64, skus []uint32) (int64, error) {
	if s.UnlockAll {
		return purchaseUnlockAll(skus)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.Load(pid)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, id := range skus {
		sku, ok := economyCatalog[id]
		if !ok {
			return 0, fmt.Errorf("unknown SKU %d", id)
		}
		price, err := SkuPrice(a, sku)
		if err != nil {
			return 0, err
		}
		if price < 0 || price > a.Balances[WumpaCurrency] {
			return 0, fmt.Errorf("insufficient Wumpa Coins")
		}
		a.Balances[WumpaCurrency] -= price
		total += price
		for _, item := range sku.Items {
			a.Items[item] = true
		}
	}
	if err := s.SaveChange(pid, a, EconomyChange{Source: "purchase", Skus: skus}); err != nil {
		return 0, err
	}
	return total, nil
}

func InventoryPage(a *EconomyAccount, page, size uint32) []uint32 {
	items := make([]uint32, 0, len(a.Items))
	for id, owned := range a.Items {
		if owned {
			items = append(items, id)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
	// CMarketplaceGetInventoryTask::start explicitly requests page 1 first.
	if page == 0 {
		return nil
	}
	start := uint64(page-1) * uint64(size)
	if start >= uint64(len(items)) {
		return nil
	}
	end := start + uint64(size)
	if end > uint64(len(items)) {
		end = uint64(len(items))
	}
	return items[start:end]
}

func (s *EconomyStore) startingBalance() int64 {
	if s.InitialBalance != nil {
		return s.InitialBalance()
	}
	return 0
}
