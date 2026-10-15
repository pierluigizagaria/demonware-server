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
	}{{18, "playera"}, {17, "PlayerB"}} {
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
}
