package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *SQLite) SteamTicketUsed(hash [32]byte) (bool, error) {
	var found int
	err := s.db.QueryRow(`SELECT 1 FROM steam_used_tickets WHERE digest = ?`, hash[:]).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// The name binding and consumed credential commit atomically. Consumed hashes
// have no short TTL: Steam tickets do not expose a verified issuance time.
func (s *SQLite) RecordSteamAdmission(hash [32]byte, pid uint64, name string, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var bound string
	err = tx.QueryRow(`SELECT name FROM steam_bindings WHERE pid = ?`, int64(pid)).Scan(&bound)
	if err == sql.ErrNoRows {
		_, err = tx.Exec(`INSERT INTO steam_bindings(pid,name,name_key) VALUES (?,?,?)`, int64(pid), name, strings.ToLower(name))
	} else if err == nil && bound != name {
		_, err = tx.Exec(`UPDATE steam_bindings SET name = ?, name_key = ? WHERE pid = ?`, name, strings.ToLower(name), int64(pid))
		if err == nil {
			if _, err := tx.Exec(`UPDATE leaderboard SET name = ? WHERE pid = ?`, name, int64(pid)); err != nil {
				return err
			}
		}
	}
	if err != nil {
		return errors.New("Steam online ID binding refused")
	}
	if _, err := tx.Exec(`INSERT INTO steam_used_tickets(digest,pid,recorded_at) VALUES (?,?,?)`, hash[:], int64(pid), at.Unix()); err != nil {
		return errors.New("Steam ticket already consumed or storage unavailable")
	}
	if _, err := tx.Exec(`INSERT INTO player_names(pid,name,name_key,recorded_at) VALUES (?,?,?,?)
		ON CONFLICT(pid) DO UPDATE SET name=excluded.name, name_key=excluded.name_key, recorded_at=excluded.recorded_at`, int64(pid), name, strings.ToLower(name), at.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
