package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver "sqlite", no cgo
)

// A nil backend retains the upstream JSON reference implementation. Runtime
// installs SQLite; economyStore.mu covers each complete load/mutate/save.
type economyBackend interface {
	load(pid uint64, create bool) (*economyAccount, error)
	save(pid uint64, a *economyAccount, change economyChange) error
}

type economyChange struct {
	source string
	skus   []uint32
}

type leaderboardStore interface {
	write(writes []ctrStatWrite) error
	read(board uint32) ([]ctrStat, error)
}

const ctrDB = "ctr.sqlite"

// ctrSQLite owns the only connection pool for this instance's economy and stats.
type ctrSQLite struct {
	db *sql.DB
}

func openCTRSQLite(path string, unlockAll bool) (*ctrSQLite, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "recursive_triggers(ON)")
	db, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &ctrSQLite{db: db}
	if err := s.initialize(unlockAll); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *ctrSQLite) Close() error { return s.db.Close() }

func (s *ctrSQLite) initialize(unlockAll bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	want := "0"
	if unlockAll {
		want = "1"
	}
	switch version {
	case 0:
		// No old files are read or imported. Schema and mode commit together.
		if _, err := tx.Exec(ctrSchema); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES ('unlock_all', ?)`, want); err != nil {
			return err
		}
		if _, err := tx.Exec(`PRAGMA user_version = 1`); err != nil {
			return err
		}
	case 1:
		var got string
		if err := tx.QueryRow(`SELECT value FROM meta WHERE key = 'unlock_all'`).Scan(&got); err != nil {
			return fmt.Errorf("read unlock-all mode: %w", err)
		}
		if got != "0" && got != "1" {
			return fmt.Errorf("invalid unlock-all mode in CTR database")
		}
		if got != want {
			return fmt.Errorf("CTR database unlock-all mode differs from CTR_UNLOCK_ALL")
		}
	default:
		return fmt.Errorf("CTR schema %d is newer than this server", version)
	}
	return tx.Commit()
}

const ctrSchema = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE accounts (
	pid INTEGER PRIMARY KEY, -- uint64 bit pattern, as in leaderboard
	created_at INTEGER NOT NULL
);
CREATE TABLE balances (
	pid INTEGER NOT NULL REFERENCES accounts(pid),
	currency INTEGER NOT NULL,
	amount INTEGER NOT NULL CHECK (amount >= 0),
	PRIMARY KEY (pid, currency)
) WITHOUT ROWID;
CREATE TABLE inventory (
	pid INTEGER NOT NULL REFERENCES accounts(pid),
	item INTEGER NOT NULL,
	source TEXT NOT NULL,
	granted_at INTEGER NOT NULL,
	PRIMARY KEY (pid, item)
) WITHOUT ROWID;
CREATE TABLE race_receipts (
	pid INTEGER NOT NULL REFERENCES accounts(pid),
	receipt TEXT NOT NULL,
	PRIMARY KEY (pid, receipt)
) WITHOUT ROWID;
CREATE TABLE online_seconds (
	pid INTEGER NOT NULL REFERENCES accounts(pid),
	day TEXT NOT NULL,
	seconds INTEGER NOT NULL,
	PRIMARY KEY (pid, day)
) WITHOUT ROWID;
CREATE TABLE challenge_progress (
	pid INTEGER NOT NULL REFERENCES accounts(pid),
	challenge TEXT NOT NULL,
	completed INTEGER NOT NULL,
	progress INTEGER NOT NULL,
	receipts TEXT NOT NULL,
	distinct_values TEXT NOT NULL,
	PRIMARY KEY (pid, challenge)
) WITHOUT ROWID;
CREATE TABLE pit_stop_refresh (
	pid INTEGER PRIMARY KEY REFERENCES accounts(pid),
	day TEXT NOT NULL,
	count INTEGER NOT NULL,
	receipts TEXT NOT NULL
);
CREATE TABLE ledger (
	id INTEGER PRIMARY KEY,
	timestamp INTEGER NOT NULL,
	pid INTEGER NOT NULL REFERENCES accounts(pid),
	source TEXT NOT NULL,
	currency INTEGER,
	delta INTEGER,
	item INTEGER,
	sku INTEGER,
	skus TEXT, -- JSON array for a purchase batch
	receipt TEXT, -- JSON array of race hashes or completed challenge keys
	CHECK ((currency IS NOT NULL AND delta IS NOT NULL AND delta != 0 AND item IS NULL)
		OR (currency IS NULL AND delta IS NULL AND item IS NOT NULL))
);
CREATE INDEX ledger_account ON ledger(pid, id);
CREATE TRIGGER inventory_no_update BEFORE UPDATE ON inventory BEGIN
	SELECT RAISE(ABORT, 'inventory is insert-only');
END;
CREATE TRIGGER inventory_no_delete BEFORE DELETE ON inventory BEGIN
	SELECT RAISE(ABORT, 'inventory is insert-only');
END;
CREATE TRIGGER ledger_no_update BEFORE UPDATE ON ledger BEGIN
	SELECT RAISE(ABORT, 'ledger is append-only');
END;
CREATE TRIGGER ledger_no_delete BEFORE DELETE ON ledger BEGIN
	SELECT RAISE(ABORT, 'ledger is append-only');
END;
CREATE TABLE leaderboard (
	board INTEGER NOT NULL,
	pid INTEGER NOT NULL,
	score INTEGER NOT NULL,
	driver INTEGER NOT NULL,
	name TEXT NOT NULL,
	updated INTEGER NOT NULL,
	PRIMARY KEY (board, pid)
) WITHOUT ROWID;
`

func newSQLiteAccount() *economyAccount {
	return &economyAccount{Version: 2, Balances: map[uint64]int64{wumpaCurrency: startingBalance()}, Items: map[uint32]bool{}}
}

// Mutating operations receive an unsaved new account, so refusal cannot create
// rows or an initial credit. A normal-economy snapshot creates it atomically;
// unlock-all snapshots remain read-only. All account tables are read consistently.
func (s *ctrSQLite) load(pid uint64, create bool) (*economyAccount, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	a, exists, err := readSQLiteAccount(tx, pid)
	if err != nil {
		return nil, err
	}
	if !exists && create {
		if err := saveSQLiteAccount(tx, pid, a, a, false, economyChange{source: "starting balance"}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a, nil
}

func accountRows(tx *sql.Tx, query string, pid uint64, scan func(*sql.Rows) error) error {
	rows, err := tx.Query(query, int64(pid))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func readSQLiteAccount(tx *sql.Tx, pid uint64) (*economyAccount, bool, error) {
	var found int
	err := tx.QueryRow(`SELECT 1 FROM accounts WHERE pid = ?`, int64(pid)).Scan(&found)
	if err == sql.ErrNoRows {
		return newSQLiteAccount(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	a := &economyAccount{Version: 2, Balances: map[uint64]int64{}, Items: map[uint32]bool{}}
	queries := []struct {
		query string
		scan  func(*sql.Rows) error
	}{
		{`SELECT currency, amount FROM balances WHERE pid = ?`, func(r *sql.Rows) error {
			var currency, amount int64
			if err := r.Scan(&currency, &amount); err != nil {
				return err
			}
			a.Balances[uint64(currency)] = amount
			return nil
		}},
		{`SELECT item FROM inventory WHERE pid = ?`, func(r *sql.Rows) error {
			var item uint32
			if err := r.Scan(&item); err != nil {
				return err
			}
			a.Items[item] = true
			return nil
		}},
		{`SELECT receipt FROM race_receipts WHERE pid = ?`, func(r *sql.Rows) error {
			var receipt string
			if err := r.Scan(&receipt); err != nil {
				return err
			}
			if a.RaceReceipts == nil {
				a.RaceReceipts = map[string]bool{}
			}
			a.RaceReceipts[receipt] = true
			return nil
		}},
		{`SELECT day, seconds FROM online_seconds WHERE pid = ?`, func(r *sql.Rows) error {
			var day string
			var seconds int64
			if err := r.Scan(&day, &seconds); err != nil {
				return err
			}
			if a.OnlineSeconds == nil {
				a.OnlineSeconds = map[string]uint64{}
			}
			a.OnlineSeconds[day] = uint64(seconds)
			return nil
		}},
		{`SELECT challenge, completed, progress, receipts, distinct_values FROM challenge_progress WHERE pid = ?`, func(r *sql.Rows) error {
			var key, receipts, distinct string
			var completed, progress int64
			if err := r.Scan(&key, &completed, &progress, &receipts, &distinct); err != nil {
				return err
			}
			c := challengeCompletion{Completed: uint64(completed), Progress: uint64(progress)}
			if err := json.Unmarshal([]byte(receipts), &c.Receipts); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(distinct), &c.Distinct); err != nil {
				return err
			}
			if a.Challenges == nil {
				a.Challenges = map[string]challengeCompletion{}
			}
			a.Challenges[key] = c
			return nil
		}},
		{`SELECT day, count, receipts FROM pit_stop_refresh WHERE pid = ?`, func(r *sql.Rows) error {
			p := &pitStopRefreshRecord{}
			var count int64
			var receipts string
			if err := r.Scan(&p.Day, &count, &receipts); err != nil {
				return err
			}
			p.Count = uint64(count)
			if err := json.Unmarshal([]byte(receipts), &p.Receipts); err != nil {
				return err
			}
			a.PitStopRefresh = p
			return nil
		}},
	}
	for _, q := range queries {
		if err := accountRows(tx, q.query, pid, q.scan); err != nil {
			return nil, false, err
		}
	}
	return a, true, nil
}

func (s *ctrSQLite) save(pid uint64, a *economyAccount, change economyChange) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, exists, err := readSQLiteAccount(tx, pid)
	if err != nil {
		return err
	}
	if err := saveSQLiteAccount(tx, pid, before, a, exists, change); err != nil {
		return err
	}
	return tx.Commit()
}

// jsonColumn only receives maps/slices of primitive types, which cannot fail to marshal.
func jsonColumn(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The account delta is the audit boundary: one row per changed currency and one
// per newly granted item. A batch keeps all known SKU/receipt references. No
// reward or pricing rules are reimplemented here.
func saveSQLiteAccount(tx *sql.Tx, pid uint64, before, after *economyAccount, exists bool, change economyChange) error {
	if after.Version != 2 || after.Balances == nil || after.Items == nil {
		return fmt.Errorf("invalid economy account")
	}
	for item, owned := range before.Items {
		if owned && !after.Items[item] {
			return fmt.Errorf("inventory is insert-only")
		}
	}
	now := time.Now().Unix()
	if !exists {
		if _, err := tx.Exec(`INSERT INTO accounts(pid, created_at) VALUES (?, ?)`, int64(pid), now); err != nil {
			return err
		}
		if amount := before.Balances[wumpaCurrency]; amount != 0 {
			if _, err := tx.Exec(`INSERT INTO ledger(timestamp, pid, source, currency, delta) VALUES (?, ?, 'starting balance', ?, ?)`, now, int64(pid), int64(wumpaCurrency), amount); err != nil {
				return err
			}
		}
	}
	var receipts []string
	for key, paid := range after.RaceReceipts {
		if paid && !before.RaceReceipts[key] {
			receipts = append(receipts, key)
		}
	}
	for key, c := range after.Challenges {
		if c.Completed != 0 && before.Challenges[key].Completed == 0 {
			receipts = append(receipts, key)
		}
	}
	sort.Strings(receipts)
	var receipt, skus, singleSKU any
	if len(receipts) != 0 {
		receipt = jsonColumn(receipts)
	}
	if len(change.skus) != 0 {
		skus = jsonColumn(change.skus)
	}
	if len(change.skus) == 1 {
		singleSKU = change.skus[0]
	}
	currencies := map[uint64]bool{}
	for c := range before.Balances {
		currencies[c] = true
	}
	for c := range after.Balances {
		currencies[c] = true
	}
	for currency := range currencies {
		old, next := before.Balances[currency], after.Balances[currency]
		if (old < 0 && next > math.MaxInt64+old) || (old > 0 && next < math.MinInt64+old) {
			return fmt.Errorf("balance delta overflow")
		}
		if delta := next - old; delta != 0 {
			if _, err := tx.Exec(`INSERT INTO ledger(timestamp, pid, source, currency, delta, sku, skus, receipt) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, now, int64(pid), change.source, int64(currency), delta, singleSKU, skus, receipt); err != nil {
				return err
			}
		}
	}
	for item, owned := range after.Items {
		if !owned || before.Items[item] {
			continue
		}
		var sku any
		for _, id := range change.skus {
			for _, granted := range economyCatalog[id].Items {
				if granted == item {
					sku = id
					break
				}
			}
			if sku != nil {
				break
			}
		}
		if _, err := tx.Exec(`INSERT INTO inventory(pid, item, source, granted_at) VALUES (?, ?, ?, ?)`, int64(pid), item, change.source, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO ledger(timestamp, pid, source, item, sku, skus, receipt) VALUES (?, ?, ?, ?, ?, ?, ?)`, now, int64(pid), change.source, item, sku, skus, receipt); err != nil {
			return err
		}
	}
	// Replace only mutable account state. Inventory and ledger are never rewritten.
	for _, table := range []string{"balances", "race_receipts", "online_seconds", "challenge_progress", "pit_stop_refresh"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE pid = ?", int64(pid)); err != nil {
			return err
		}
	}
	for currency, amount := range after.Balances {
		if _, err := tx.Exec(`INSERT INTO balances(pid, currency, amount) VALUES (?, ?, ?)`, int64(pid), int64(currency), amount); err != nil {
			return err
		}
	}
	for receipt, paid := range after.RaceReceipts {
		if !paid {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO race_receipts(pid, receipt) VALUES (?, ?)`, int64(pid), receipt); err != nil {
			return err
		}
	}
	for day, seconds := range after.OnlineSeconds {
		if _, err := tx.Exec(`INSERT INTO online_seconds(pid, day, seconds) VALUES (?, ?, ?)`, int64(pid), day, int64(seconds)); err != nil {
			return err
		}
	}
	for key, c := range after.Challenges {
		if _, err := tx.Exec(`INSERT INTO challenge_progress(pid, challenge, completed, progress, receipts, distinct_values) VALUES (?, ?, ?, ?, ?, ?)`, int64(pid), key, int64(c.Completed), int64(c.Progress), jsonColumn(c.Receipts), jsonColumn(c.Distinct)); err != nil {
			return err
		}
	}
	if p := after.PitStopRefresh; p != nil {
		if _, err := tx.Exec(`INSERT INTO pit_stop_refresh(pid, day, count, receipts) VALUES (?, ?, ?, ?)`, int64(pid), p.Day, int64(p.Count), jsonColumn(p.Receipts)); err != nil {
			return err
		}
	}
	return nil
}

func (s *ctrSQLite) write(writes []ctrStatWrite) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range writes {
		var old int64
		err := tx.QueryRow(`SELECT score FROM leaderboard WHERE board = ? AND pid = ?`, w.Board, int64(w.PID)).Scan(&old)
		exists := err == nil
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		next := w.ctrStat
		switch w.Operation {
		case 1:
			if (w.Score > 0 && old > math.MaxInt64-w.Score) || (w.Score < 0 && old < math.MinInt64-w.Score) {
				return fmt.Errorf("score overflow")
			}
			next.Score += old
		case 4:
			// CTR encodes elapsed time into a descending uint32 score: faster is larger.
			if exists && old >= w.Score {
				continue
			}
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO leaderboard (board, pid, score, driver, name, updated) VALUES (?, ?, ?, ?, ?, ?)`,
			next.Board, int64(next.PID), next.Score, next.Driver, next.Name, next.Updated); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ctrSQLite) read(board uint32) ([]ctrStat, error) {
	rs, err := s.db.Query(`SELECT pid, score, driver, name, updated FROM leaderboard WHERE board = ?`, board)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	rows := []ctrStat{}
	for rs.Next() {
		r := ctrStat{Board: board}
		var pid int64
		if err := rs.Scan(&pid, &r.Score, &r.Driver, &r.Name, &r.Updated); err != nil {
			return nil, err
		}
		r.PID = uint64(pid)
		rows = append(rows, r)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	// Same order as the JSON store: score descending, then account ID.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].PID < rows[j].PID
		}
		return rows[i].Score > rows[j].Score
	})
	for i := range rows {
		rows[i].Rank = uint64(i + 1)
	}
	return rows, nil
}
