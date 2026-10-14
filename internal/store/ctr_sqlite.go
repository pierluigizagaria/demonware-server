package store

import (
	sql "database/sql"
	json "encoding/json"
	fmt "fmt"
	ctrpkg "github.com/NextendoNetwork/nextendo-demonware/internal/ctr"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	math "math"
	_ "modernc.org/sqlite"
	url "net/url"
	os "os"
	reflect "reflect"
	sort "sort"
	strings "strings"
	time "time"
)

const CtrDB = "ctrnf.db"

const CtrSchemaVersion = 1

// awardRaces accepts events from the last seven days, five minutes ahead at
// most. A race receipt or a day's online seconds last written more than nine
// days ago can no longer affect a reward, so it is neither loaded nor kept.
const ReplayRetention = 9 * 24 * time.Hour

// ctrSQLite owns the only connection pool for this instance's economy, stats
// and player names.
type CtrSQLite struct {
	InitialBalance func() int64
	Clock          func() time.Time
	Db             *sql.DB
	lock           *os.File
}

// Economy saves read and write in separate transactions under a process mutex,
// so a second process on the same database could lose updates. An OS lock on
// a sibling file, released when the process exits, refuses a second instance.
// Read-only tools can still open the database itself.
func lockCTRInstance(path string) (*os.File, error) {
	f, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s is in use by another Nextendo instance", path)
	}
	return f, nil
}

// openCTRSQLite binds the instance to its admission namespace: the shadNet
// issuer/client/title or the separate anonymous-guest namespace.
func OpenCTRSQLite(path string, unlockAll bool, trustID string) (*CtrSQLite, error) {
	if trustID == "" {
		return nil, fmt.Errorf("CTR database needs a trust ID")
	}
	lock, err := lockCTRInstance(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "recursive_triggers(ON)")
	db, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &CtrSQLite{Db: db, lock: lock}
	if err := s.initialize(unlockAll, trustID); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *CtrSQLite) Close() error {
	err := s.Db.Close()
	s.lock.Close() // releases the instance lock; repeated closes are harmless
	return err
}

func (s *CtrSQLite) initialize(unlockAll bool, trustID string) error {
	tx, err := s.Db.Begin()
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
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES ('unlock_all', ?), ('trust_id', ?)`, want, trustID); err != nil {
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, CtrSchemaVersion)); err != nil {
			return err
		}
	case CtrSchemaVersion:
		var trusted string
		if err := tx.QueryRow(`SELECT value FROM meta WHERE key = 'trust_id'`).Scan(&trusted); err != nil {
			return fmt.Errorf("read trust ID: %w", err)
		}
		if trusted != trustID {
			return fmt.Errorf("CTR database belongs to another admission identity namespace")
		}
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
		return fmt.Errorf("CTR schema %d is not supported by this server", version)
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
	recorded_at INTEGER NOT NULL,
	PRIMARY KEY (pid, receipt)
) WITHOUT ROWID;
CREATE TABLE online_seconds (
	pid INTEGER NOT NULL REFERENCES accounts(pid),
	day TEXT NOT NULL,
	seconds INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
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
CREATE INDEX leaderboard_rank ON leaderboard(board, score DESC, pid < 0, pid);
-- The latest name each player was issued a ticket under. It only answers
-- 12/10 lookups: admission never reads it.
CREATE TABLE player_names (
	pid INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	name_key TEXT NOT NULL, -- lower-case name, as 12/10 compares
	recorded_at INTEGER NOT NULL
);
CREATE INDEX player_names_lookup ON player_names(name_key, recorded_at DESC);
`

func newSQLiteAccount(balance int64) *ctrpkg.EconomyAccount {
	return &ctrpkg.EconomyAccount{Version: 2, Balances: map[uint64]int64{ctrpkg.WumpaCurrency: balance}, Items: map[uint32]bool{}}
}

// Mutating operations receive an unsaved new account, so refusal cannot create
// rows or an initial credit. A normal-economy snapshot creates it atomically;
// unlock-all snapshots remain read-only. All account tables are read consistently.
func (s *CtrSQLite) Load(pid uint64, create bool) (*ctrpkg.EconomyAccount, error) {
	tx, err := s.Db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	a, exists, err := readSQLiteAccount(tx, pid, s.now, s.StartingBalance)
	if err != nil {
		return nil, err
	}
	if !exists && create {
		if err := saveSQLiteAccount(tx, pid, a, a, false, ctrpkg.EconomyChange{Source: "starting balance"}, s.now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a, nil
}

func accountRows(tx *sql.Tx, query string, scan func(*sql.Rows) error, args ...any) error {
	rows, err := tx.Query(query, args...)
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

func readSQLiteAccount(tx *sql.Tx, pid uint64, clock func() time.Time, balance func() int64) (*ctrpkg.EconomyAccount, bool, error) {
	var found int
	err := tx.QueryRow(`SELECT 1 FROM accounts WHERE pid = ?`, int64(pid)).Scan(&found)
	if err == sql.ErrNoRows {
		return newSQLiteAccount(balance()), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	a := &ctrpkg.EconomyAccount{Version: 2, Balances: map[uint64]int64{}, Items: map[uint32]bool{}}
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
		{`SELECT receipt FROM race_receipts WHERE pid = ? AND recorded_at >= ?`, func(r *sql.Rows) error {
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
		{`SELECT day, seconds FROM online_seconds WHERE pid = ? AND updated_at >= ?`, func(r *sql.Rows) error {
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
			c := ctrpkg.ChallengeCompletion{Completed: uint64(completed), Progress: uint64(progress)}
			if err := json.Unmarshal([]byte(receipts), &c.Receipts); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(distinct), &c.Distinct); err != nil {
				return err
			}
			if a.Challenges == nil {
				a.Challenges = map[string]ctrpkg.ChallengeCompletion{}
			}
			a.Challenges[key] = c
			return nil
		}},
		{`SELECT day, count, receipts FROM pit_stop_refresh WHERE pid = ?`, func(r *sql.Rows) error {
			p := &ctrpkg.PitStopRefreshRecord{}
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
	retained := clock().Add(-ReplayRetention).Unix()
	for _, q := range queries {
		// Replay-state queries take the retention cutoff as a second parameter.
		args := []any{int64(pid), retained}[:strings.Count(q.query, "?")]
		if err := accountRows(tx, q.query, q.scan, args...); err != nil {
			return nil, false, err
		}
	}
	return a, true, nil
}

func (s *CtrSQLite) Save(pid uint64, a *ctrpkg.EconomyAccount, change ctrpkg.EconomyChange) error {
	tx, err := s.Db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, exists, err := readSQLiteAccount(tx, pid, s.now, s.StartingBalance)
	if err != nil {
		return err
	}
	if err := saveSQLiteAccount(tx, pid, before, a, exists, change, s.now); err != nil {
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
func saveSQLiteAccount(tx *sql.Tx, pid uint64, before, after *ctrpkg.EconomyAccount, exists bool, change ctrpkg.EconomyChange, clock func() time.Time) error {
	if after.Version != 2 || after.Balances == nil || after.Items == nil {
		return fmt.Errorf("invalid economy account")
	}
	for item, owned := range before.Items {
		if owned && !after.Items[item] {
			return fmt.Errorf("inventory is insert-only")
		}
	}
	now := clock().Unix()
	if !exists {
		if _, err := tx.Exec(`INSERT INTO accounts(pid, created_at) VALUES (?, ?)`, int64(pid), now); err != nil {
			return err
		}
		if amount := before.Balances[ctrpkg.WumpaCurrency]; amount != 0 {
			if _, err := tx.Exec(`INSERT INTO ledger(timestamp, pid, source, currency, delta) VALUES (?, ?, 'starting balance', ?, ?)`, now, int64(pid), int64(ctrpkg.WumpaCurrency), amount); err != nil {
				return err
			}
		}
	}
	// Rows written before this account existed are not stored state.
	stored := before
	if !exists {
		stored = &ctrpkg.EconomyAccount{}
	}
	var receipts, newReceipts []string
	for key, paid := range after.RaceReceipts {
		if !paid || before.RaceReceipts[key] {
			continue
		}
		// A receipt that just left the retention window is not new.
		var found int
		err := tx.QueryRow(`SELECT 1 FROM race_receipts WHERE pid = ? AND receipt = ?`, int64(pid), key).Scan(&found)
		if err == sql.ErrNoRows {
			receipts, newReceipts = append(receipts, key), append(newReceipts, key)
		} else if err != nil {
			return err
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
	if len(change.Skus) != 0 {
		skus = jsonColumn(change.Skus)
	}
	if len(change.Skus) == 1 {
		singleSKU = change.Skus[0]
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
			if _, err := tx.Exec(`INSERT INTO ledger(timestamp, pid, source, currency, delta, sku, skus, receipt) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, now, int64(pid), change.Source, int64(currency), delta, singleSKU, skus, receipt); err != nil {
				return err
			}
		}
	}
	for item, owned := range after.Items {
		if !owned || before.Items[item] {
			continue
		}
		var sku any
		for _, id := range change.Skus {
			for _, granted := range ctrpkg.EconomySKUItems(id) {
				if granted == item {
					sku = id
					break
				}
			}
			if sku != nil {
				break
			}
		}
		if _, err := tx.Exec(`INSERT INTO inventory(pid, item, source, granted_at) VALUES (?, ?, ?, ?)`, int64(pid), item, change.Source, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO ledger(timestamp, pid, source, item, sku, skus, receipt) VALUES (?, ?, ?, ?, ?, ?, ?)`, now, int64(pid), change.Source, item, sku, skus, receipt); err != nil {
			return err
		}
	}
	// Write only changed mutable rows. Inventory and ledger are never rewritten.
	exec := func(query string, args ...any) error {
		_, err := tx.Exec(query, append([]any{int64(pid)}, args...)...)
		return err
	}
	for currency := range currencies {
		old, had := stored.Balances[currency]
		next, has := after.Balances[currency]
		var err error
		switch {
		case !has:
			err = exec(`DELETE FROM balances WHERE pid = ? AND currency = ?`, int64(currency))
		case !had || old != next:
			err = exec(`INSERT INTO balances(pid, currency, amount) VALUES (?, ?, ?)
				ON CONFLICT(pid, currency) DO UPDATE SET amount = excluded.amount`, int64(currency), next)
		}
		if err != nil {
			return err
		}
	}
	for _, key := range newReceipts {
		if err := exec(`INSERT INTO race_receipts(pid, receipt, recorded_at) VALUES (?, ?, ?)`, key, now); err != nil {
			return err
		}
	}
	for key, paid := range stored.RaceReceipts {
		if paid && !after.RaceReceipts[key] {
			if err := exec(`DELETE FROM race_receipts WHERE pid = ? AND receipt = ?`, key); err != nil {
				return err
			}
		}
	}
	for day, seconds := range after.OnlineSeconds {
		if old, had := stored.OnlineSeconds[day]; !had || old != seconds {
			if err := exec(`INSERT INTO online_seconds(pid, day, seconds, updated_at) VALUES (?, ?, ?, ?)
				ON CONFLICT(pid, day) DO UPDATE SET seconds = excluded.seconds, updated_at = excluded.updated_at`, day, int64(seconds), now); err != nil {
				return err
			}
		}
	}
	for day := range stored.OnlineSeconds {
		if _, has := after.OnlineSeconds[day]; !has {
			if err := exec(`DELETE FROM online_seconds WHERE pid = ? AND day = ?`, day); err != nil {
				return err
			}
		}
	}
	for key, c := range after.Challenges {
		if old, had := stored.Challenges[key]; !had || !reflect.DeepEqual(old, c) {
			if err := exec(`INSERT INTO challenge_progress(pid, challenge, completed, progress, receipts, distinct_values) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(pid, challenge) DO UPDATE SET completed = excluded.completed, progress = excluded.progress,
				receipts = excluded.receipts, distinct_values = excluded.distinct_values`,
				key, int64(c.Completed), int64(c.Progress), jsonColumn(c.Receipts), jsonColumn(c.Distinct)); err != nil {
				return err
			}
		}
	}
	for key := range stored.Challenges {
		if _, has := after.Challenges[key]; !has {
			if err := exec(`DELETE FROM challenge_progress WHERE pid = ? AND challenge = ?`, key); err != nil {
				return err
			}
		}
	}
	if p := after.PitStopRefresh; p == nil && stored.PitStopRefresh != nil {
		if err := exec(`DELETE FROM pit_stop_refresh WHERE pid = ?`); err != nil {
			return err
		}
	} else if p != nil && !reflect.DeepEqual(p, stored.PitStopRefresh) {
		if err := exec(`INSERT INTO pit_stop_refresh(pid, day, count, receipts) VALUES (?, ?, ?, ?)
			ON CONFLICT(pid) DO UPDATE SET day = excluded.day, count = excluded.count, receipts = excluded.receipts`,
			p.Day, int64(p.Count), jsonColumn(p.Receipts)); err != nil {
			return err
		}
	}
	// Drop replay state that can no longer affect a reward.
	expired := clock().Add(-ReplayRetention).Unix()
	if err := exec(`DELETE FROM race_receipts WHERE pid = ? AND recorded_at < ?`, expired); err != nil {
		return err
	}
	return exec(`DELETE FROM online_seconds WHERE pid = ? AND updated_at < ?`, expired)
}

func (s *CtrSQLite) Write(writes []ctrpkg.CtrStatWrite) error {
	tx, err := s.Db.Begin()
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
		next := w.CtrStat
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

func (s *CtrSQLite) Read(board uint32) ([]ctrpkg.CtrStat, error) {
	rs, err := s.Db.Query(`SELECT pid, score, driver, name, updated FROM leaderboard WHERE board = ?`, board)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	rows := []ctrpkg.CtrStat{}
	for rs.Next() {
		r := ctrpkg.CtrStat{Board: board}
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

// Ranking order shared with read: score descending, then the unsigned account
// ID. A signed PID column orders unsigned IDs by (pid < 0, pid); the
// leaderboard_rank index has the same expression, so pages are index scans.
const LeaderboardOrder = `score DESC, pid < 0, pid`

func (s *CtrSQLite) ReadRanks(board uint32, start uint64, limit uint32) ([]ctrpkg.CtrStat, uint64, error) {
	tx, err := s.Db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total uint64
	if err := tx.QueryRow(`SELECT count(*) FROM leaderboard WHERE board = ?`, board).Scan(&total); err != nil {
		return nil, 0, err
	}
	if start == 0 {
		start = 1
	}
	rows := []ctrpkg.CtrStat{}
	if start-1 >= total || limit == 0 {
		return rows, total, nil
	}
	rs, err := tx.Query(`SELECT pid, score, driver, name, updated FROM leaderboard WHERE board = ?
		ORDER BY `+LeaderboardOrder+` LIMIT ? OFFSET ?`, board, limit, int64(start-1))
	if err != nil {
		return nil, 0, err
	}
	defer rs.Close()
	for rs.Next() {
		r := ctrpkg.CtrStat{Board: board, Rank: start + uint64(len(rows))}
		var pid int64
		if err := rs.Scan(&pid, &r.Score, &r.Driver, &r.Name, &r.Updated); err != nil {
			return nil, 0, err
		}
		r.PID = uint64(pid)
		rows = append(rows, r)
	}
	return rows, total, rs.Err()
}

// readEntities returns the given accounts' rows in rank order; each rank
// counts the rows ordered before it.
func (s *CtrSQLite) ReadEntities(board uint32, pids []uint64) ([]ctrpkg.CtrStat, error) {
	rows := []ctrpkg.CtrStat{}
	if len(pids) == 0 {
		return rows, nil
	}
	args := []any{board}
	for _, pid := range pids {
		args = append(args, int64(pid))
	}
	rs, err := s.Db.Query(`SELECT l.pid, l.score, l.driver, l.name, l.updated,
		1 + (SELECT count(*) FROM leaderboard o WHERE o.board = l.board AND (o.score > l.score
			OR (o.score = l.score AND ((o.pid < 0) < (l.pid < 0) OR ((o.pid < 0) = (l.pid < 0) AND o.pid < l.pid)))))
		FROM leaderboard l WHERE l.board = ? AND l.pid IN (?`+strings.Repeat(", ?", len(pids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		r := ctrpkg.CtrStat{Board: board}
		var pid int64
		if err := rs.Scan(&pid, &r.Score, &r.Driver, &r.Name, &r.Updated, &r.Rank); err != nil {
			return nil, err
		}
		r.PID = uint64(pid)
		rows = append(rows, r)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Rank < rows[j].Rank })
	return rows, nil
}

func (s *CtrSQLite) RememberName(pid uint64, name string, at time.Time) error {
	_, err := s.Db.Exec(`INSERT INTO player_names(pid, name, name_key, recorded_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(pid) DO UPDATE SET name = excluded.name, name_key = excluded.name_key, recorded_at = excluded.recorded_at`,
		int64(pid), name, strings.ToLower(name), at.Unix())
	return err
}

// namedPlayers returns, by lower-case name, the player most recently recorded
// under each requested name.
func (s *CtrSQLite) NamedPlayers(names []string) (map[string]*platformpkg.PlayerID, error) {
	out := map[string]*platformpkg.PlayerID{}
	for _, name := range names {
		key := strings.ToLower(name)
		if out[key] != nil {
			continue
		}
		var pid int64
		var stored string
		err := s.Db.QueryRow(`SELECT pid, name FROM player_names WHERE name_key = ?
			ORDER BY recorded_at DESC, pid DESC LIMIT 1`, key).Scan(&pid, &stored)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[key] = &platformpkg.PlayerID{PID: uint64(pid), Username: stored}
	}
	return out, nil
}

func (s *CtrSQLite) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *CtrSQLite) StartingBalance() int64 {
	if s.InitialBalance != nil {
		return s.InitialBalance()
	}
	return 0
}
