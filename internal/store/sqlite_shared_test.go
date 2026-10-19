package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/platform/guest"
)

const sharedTrust = "ctrnf:local:v2:steam"

func legacySteamTrust(trust string) bool {
	return strings.HasPrefix(trust, "ctrnf:local:480:5760:v1:steam:") && trust != "ctrnf:local:480:5760:v1:steam:bad"
}

func openShared(t *testing.T, path string, initialBalance int64) *SQLite {
	t.Helper()
	db, err := OpenSharedSQLite(path, sharedTrust, legacySteamTrust)
	if err != nil {
		t.Fatal(err)
	}
	db.InitialBalance = func() int64 { return initialBalance }
	t.Cleanup(func() { db.Close() })
	return db
}

func sharedEconomy(db *SQLite, dir string, unlockAll bool, initialBalance int64) *ctr.EconomyStore {
	return &ctr.EconomyStore{InitialBalance: func() int64 { return initialBalance }, Dir: dir, Backend: db, UnlockAll: unlockAll}
}

// Every row of every table except the binding, in a stable order.
func sqliteRows(t *testing.T, db *SQLite) map[string][]string {
	t.Helper()
	tables := map[string][]string{}
	names, err := db.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name != 'meta' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var list []string
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			t.Fatal(err)
		}
		list = append(list, name)
	}
	names.Close()
	for _, name := range list {
		rows, err := db.db.Query("SELECT * FROM " + name + " ORDER BY 1, 2")
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			tables[name] = append(tables[name], fmt.Sprint(values...))
		}
		rows.Close()
	}
	return tables
}

func sqliteMeta(t *testing.T, db *SQLite) map[string]string {
	t.Helper()
	rows, err := db.db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		meta[key] = value
	}
	return meta
}

// One local database serves A, then B, then A again, across both economy
// modes. Unlock-all adds availability without persisting catalog grants.
func TestSharedDatabaseAcrossAccountsAndModes(t *testing.T) {
	const initial, a, b = int64(10000), uint64(76561198000000001), uint64(76561198000000002)
	dir := t.TempDir()
	path := filepath.Join(dir, DBFile)
	price := ctr.EconomyCatalogSnapshot()[200085].Price
	db := openShared(t, path, initial)
	if meta := sqliteMeta(t, db); !reflect.DeepEqual(meta, map[string]string{"trust_id": sharedTrust}) {
		t.Fatalf("shared binding: %v", meta)
	}
	normal := sharedEconomy(db, dir, false, initial)
	if _, err := normal.Purchase(a, []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db = openShared(t, path, initial)
	normal = sharedEconomy(db, dir, false, initial)
	if got, err := normal.Snapshot(b); err != nil || got.Balances[ctr.WumpaCurrency] != initial || len(got.Items) != 0 {
		t.Fatalf("second account inherited the first: %+v, %v", got, err)
	}
	db.Close()

	db = openShared(t, path, initial)
	unlocked := sharedEconomy(db, dir, true, initial)
	before := sqliteRows(t, db)
	inventory, err := unlocked.Inventory(a)
	if err != nil || !reflect.DeepEqual(inventory.Items, ctr.CatalogItemsSnapshot()) {
		t.Fatal("unlock-all inventory is not the catalog")
	}
	if debit, err := unlocked.Purchase(a, []uint32{200597}); err != nil || debit != 0 {
		t.Fatalf("unlock-all purchase = %d, %v", debit, err)
	}
	if !reflect.DeepEqual(before, sqliteRows(t, db)) {
		t.Fatal("unlock-all persisted catalog state")
	}
	db.Close()

	db = openShared(t, path, initial)
	normal = sharedEconomy(db, dir, false, initial)
	got, err := normal.Inventory(a)
	if err != nil || len(got.Items) != len(ctr.EconomyCatalogSnapshot()[200085].Items) || got.Balances[ctr.WumpaCurrency] != initial-price {
		t.Fatalf("returning to normal lost or widened ownership: %+v, %v", got, err)
	}
	if ctr.OwnsSKU(got, ctr.EconomyCatalogSnapshot()[200597]) {
		t.Fatal("unlock-all item became owned")
	}
}

// Race rewards are real progression in both modes. A first reward recorded in
// unlock-all still creates the account's starting balance.
func TestSharedUnlockAllFirstRewardKeepsStartingBalance(t *testing.T) {
	const initial, pid = int64(321), uint64(42)
	dir := t.TempDir()
	path := filepath.Join(dir, DBFile)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	db := openShared(t, path, initial)
	credit, err := sharedEconomy(db, dir, true, initial).AwardRaces(pid, []ctr.AchievementEvent{capturedRace(now)}, now)
	if err != nil || credit <= 0 {
		t.Fatalf("reward = %d, %v", credit, err)
	}
	var sources []string
	rows, err := db.db.Query(`SELECT source FROM ledger WHERE pid = ? ORDER BY id`, int64(pid))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source)
	}
	rows.Close()
	if len(sources) != 2 || sources[0] != "starting balance" {
		t.Fatalf("ledger sources: %v", sources)
	}
	db.Close()
	db = openShared(t, path, initial)
	got, err := sharedEconomy(db, dir, false, initial).Snapshot(pid)
	if err != nil || got.Balances[ctr.WumpaCurrency] != initial+credit {
		t.Fatalf("normal balance after unlock-all reward: %+v, %v", got, err)
	}
}

// A database written under the v1 owner-, AppID-, realm- and mode-bound
// binding keeps every row and becomes the shared binding.
func TestSharedDatabaseUpgradesLegacyLocalBinding(t *testing.T) {
	for _, unlockAll := range []bool{false, true} {
		t.Run(fmt.Sprint(unlockAll), func(t *testing.T) {
			const initial = int64(10000)
			dir := t.TempDir()
			path := filepath.Join(dir, DBFile)
			legacy, err := OpenSQLite(path, unlockAll, "ctrnf:local:480:5760:v1:steam:ctrnf-p2p:76561198000000001")
			if err != nil {
				t.Fatal(err)
			}
			legacy.InitialBalance = func() int64 { return initial }
			now := time.Now()
			economy := sharedEconomy(legacy, dir, false, initial)
			if _, err := economy.Purchase(7, []uint32{200085}); err != nil {
				t.Fatal(err)
			}
			if err := legacy.Write([]ctr.StatWrite{{Stat: ctr.Stat{Board: 89, PID: 7, Score: 50, Name: "Fixture"}, Operation: 1}}); err != nil {
				t.Fatal(err)
			}
			if err := legacy.RememberName(7, "Fixture", now); err != nil {
				t.Fatal(err)
			}
			before := sqliteRows(t, legacy)
			legacy.Close()

			db := openShared(t, path, initial)
			if meta := sqliteMeta(t, db); !reflect.DeepEqual(meta, map[string]string{"trust_id": sharedTrust}) {
				t.Fatalf("upgraded binding: %v", meta)
			}
			if !reflect.DeepEqual(before, sqliteRows(t, db)) {
				t.Fatal("upgrade changed account or history rows")
			}
			db.Close()
			if old, err := OpenSQLite(path, unlockAll, "ctrnf:local:480:5760:v1:steam:ctrnf-p2p:76561198000000001"); err == nil {
				old.Close()
				t.Fatal("pre-upgrade binding still opens the upgraded database")
			}
		})
	}
}

func TestSharedDatabaseRefusesOtherBindings(t *testing.T) {
	cases := map[string]func(path string) error{
		"guest namespace": func(path string) error {
			db, err := OpenSQLite(path, true, guest.TrustID)
			if err == nil {
				db.Close()
			}
			return err
		},
		"invalid legacy": func(path string) error {
			db, err := OpenSQLite(path, false, "ctrnf:local:480:5760:v1:steam:bad")
			if err == nil {
				db.Close()
			}
			return err
		},
	}
	for name, create := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), DBFile)
			if err := create(path); err != nil {
				t.Fatal(err)
			}
			if db, err := OpenSharedSQLite(path, sharedTrust, legacySteamTrust); err == nil {
				db.Close()
				t.Fatal("shared open accepted another binding")
			}
			// The refusal left the original binding usable.
			if err := create(path); err != nil {
				t.Fatalf("refused database changed: %v", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), DBFile)
	openShared(t, path, 0).Close()
	for _, unlockAll := range []bool{false, true} {
		if db, err := OpenSQLite(path, unlockAll, sharedTrust); err == nil {
			db.Close()
			t.Fatal("mode-bound open accepted a shared database")
		}
	}
	db := openShared(t, path, 0)
	sqliteExec(t, db, `INSERT INTO meta(key, value) VALUES ('unlock_all', '0')`)
	db.Close()
	if reopened, err := OpenSharedSQLite(path, sharedTrust, legacySteamTrust); err == nil {
		reopened.Close()
		t.Fatal("shared database with a mode row accepted")
	}
}
