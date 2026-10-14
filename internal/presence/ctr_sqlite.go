package presence

import (
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	time "time"
)

// playerNameStore remembers the names players were issued a ticket under,
// so 12/10 resolves a friend who has not connected yet. Runtime installs
// SQLite.
type playerNameStore interface {
	RememberName(pid uint64, name string, at time.Time) error
	NamedPlayers(names []string) (map[string]*platformpkg.PlayerID, error)
}

func (state *PresenceStore) RememberPlayerName(id platformpkg.PlayerID, at time.Time) error {
	if state.PlayerNames == nil || id.PID == 0 || id.Username == "" {
		return nil
	}
	return state.PlayerNames.RememberName(id.PID, id.Username, at)
}
