package presence

import (
	"sync"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

type localNamesFixture struct{ name string }

func (s *localNamesFixture) RememberName(_ uint64, name string, _ time.Time) error {
	s.name = name
	return nil
}
func (s *localNamesFixture) NamedPlayers([]string) (map[string]*platform.PlayerID, error) {
	return nil, nil
}

func TestTrustedLocalRenameKeepsSessionsAndResistsLateTickets(t *testing.T) {
	names := &localNamesFixture{}
	state := NewStore(names)
	old := platform.PlayerID{PID: 17, Username: "Initial", Kind: "local", Title: 5760, Sub: "local:fixture"}
	state.RegisterOnline(1, &old)
	current := old
	current.Username = "Renamed"
	if err := state.SetLocalOwnerName(current, time.Now()); err != nil {
		t.Fatal(err)
	}
	if old.Username != "Initial" || state.ConnectionPlayer(1).Username != "Renamed" || state.Counts().Online != 1 {
		t.Fatal("session was mutated or removed")
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(conn uint64) {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				state.RegisterOnline(conn, &old)
				if err := state.RememberPlayerName(old, time.Now()); err != nil {
					t.Error(err)
				}
				if err := state.SetLocalOwnerName(current, time.Now()); err != nil {
					t.Error(err)
				}
				if state.ConnectionPlayer(conn).Username != "Renamed" {
					t.Error("old ticket reverted name")
				}
			}
		}(uint64(i + 2))
	}
	workers.Wait()
	if names.name != "Renamed" || state.Counts().Online != 9 {
		t.Fatal("name persistence or session count changed")
	}
	for _, id := range state.Snapshot("ctr") {
		if id.PID != old.PID || id.Sub != old.Sub || id.Username != "Renamed" {
			t.Fatal("identity changed")
		}
	}
	other := current
	other.PID++
	if state.SetLocalOwnerName(other, time.Now()) == nil {
		t.Fatal("second local owner accepted")
	}
}
