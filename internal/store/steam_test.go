package store

import (
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"
)

func TestSteamReplayAndNameBindingPersistTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), DBFile)
	open := func() *SQLite {
		s, err := OpenSQLite(path, false, "ctrnf:steam:480:5760:v1:normal")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	hash := sha256.Sum256([]byte("credential-one"))
	if err := s.RecordSteamAdmission(hash, 17, "PlayerA", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSteamAdmission(hash, 17, "PlayerA", time.Now()); err == nil {
		t.Fatal("replay accepted")
	}
	s.Close()
	s = open()
	defer s.Close()
	if used, err := s.SteamTicketUsed(hash); err != nil || !used {
		t.Fatal("replay memory lost on restart")
	}
	other := sha256.Sum256([]byte("credential-two"))
	for _, test := range []struct {
		id   uint64
		name string
	}{{18, "playera"}} {
		if err := s.RecordSteamAdmission(other, test.id, test.name, time.Now()); err == nil {
			t.Fatal("conflicting name binding accepted")
		}
		if used, _ := s.SteamTicketUsed(other); used {
			t.Fatal("refused binding consumed credential")
		}
	}
	if err := s.RecordSteamAdmission(other, 18, "PlayerB", time.Now()); err != nil {
		t.Fatal(err)
	}
	names, err := s.NamedPlayers([]string{"playera", "playerb"})
	if err != nil || len(names) != 2 || names["playera"].PID != 17 || names["playerb"].PID != 18 {
		t.Fatal("friend lookup not persisted")
	}
	if _, err := s.db.Exec(`INSERT INTO leaderboard (board, pid, score, driver, name, updated) VALUES (1000, 17, 5, 0, 'PlayerA', 0)`); err != nil {
		t.Fatal(err)
	}
	renamed := sha256.Sum256([]byte("credential-three"))
	if err := s.RecordSteamAdmission(renamed, 17, "Pi\u00e9 Z.", time.Now()); err != nil {
		t.Fatal("same Steam account could not follow its renamed persona:", err)
	}
	names, err = s.NamedPlayers([]string{"playera", "pi\u00e9 z."})
	if err != nil || names["playera"] != nil || names["pi\u00e9 z."] == nil || names["pi\u00e9 z."].PID != 17 {
		t.Fatal("rename did not move the friend lookup to the new name")
	}
	var board string
	if err := s.db.QueryRow(`SELECT name FROM leaderboard WHERE board = 1000 AND pid = 17`).Scan(&board); err != nil || board != "Pi\u00e9 Z." {
		t.Fatal("rename did not update the player's leaderboard name")
	}
	freed := sha256.Sum256([]byte("credential-four"))
	if err := s.RecordSteamAdmission(freed, 19, "PlayerA", time.Now()); err != nil {
		t.Fatal("name released by a rename stayed reserved:", err)
	}
	taken := sha256.Sum256([]byte("credential-five"))
	if err := s.RecordSteamAdmission(taken, 18, "playera", time.Now()); err == nil {
		t.Fatal("rename onto another account's name accepted")
	}
}
