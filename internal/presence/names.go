package presence

import (
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

// playerNameStore remembers the names players were issued a ticket under,
// so 12/10 resolves a friend who has not connected yet. Runtime installs
// SQLite.
type playerNameStore interface {
	RememberName(pid uint64, name string, at time.Time) error
	NamedPlayers(names []string) (map[string]*platform.PlayerID, error)
}

func (state *Store) RememberPlayerName(id platform.PlayerID, at time.Time) error {
	if state.PlayerNames == nil || id.PID == 0 || id.Username == "" {
		return nil
	}
	return state.PlayerNames.RememberName(id.PID, id.Username, at)
}
