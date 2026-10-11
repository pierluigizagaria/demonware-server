package main

import (
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"sort"

	_ "modernc.org/sqlite" // pure-Go driver "sqlite", no cgo
)

// leaderboardStore persists bdStats rows. The upstream JSON file store and the
// SQLite store apply the same write operations and ranking.
type leaderboardStore interface {
	write(writes []ctrStatWrite) error
	read(board uint32) ([]ctrStat, error)
}

const leaderboardDB = "ctr-leaderboards.sqlite"

// sqliteLeaderboard keeps one row per (board, player) in a database owned by
// this instance. A batch of writes commits in one transaction, or not at all.
type sqliteLeaderboard struct {
	db *sql.DB
}

func openSQLiteLeaderboard(path string) (*sqliteLeaderboard, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	db, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// One connection serializes writers, like the JSON store's mutex.
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err == nil {
		switch version {
		case 0:
			_, err = db.Exec(`CREATE TABLE IF NOT EXISTS leaderboard (
				board   INTEGER NOT NULL,
				pid     INTEGER NOT NULL, -- uint64 bit pattern
				score   INTEGER NOT NULL,
				driver  INTEGER NOT NULL,
				name    TEXT    NOT NULL,
				updated INTEGER NOT NULL,
				PRIMARY KEY (board, pid)
			) WITHOUT ROWID; PRAGMA user_version = 1`)
		case 1:
		default:
			err = fmt.Errorf("leaderboard schema %d is newer than this server", version)
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteLeaderboard{db: db}, nil
}

func (s *sqliteLeaderboard) Close() error { return s.db.Close() }

func (s *sqliteLeaderboard) write(writes []ctrStatWrite) error {
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

func (s *sqliteLeaderboard) read(board uint32) ([]ctrStat, error) {
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
