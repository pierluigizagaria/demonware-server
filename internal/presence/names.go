package presence

import (
	"errors"
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
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	if current := state.localOwnerName; current != nil && current.PID == id.PID {
		id.Username = current.Username
	}
	return state.rememberName(id, at)
}

func (state *Store) rememberName(id platform.PlayerID, at time.Time) error {
	if state.playerNames == nil || id.PID == 0 || id.Username == "" {
		return nil
	}
	return state.playerNames.RememberName(id.PID, id.Username, at)
}

// SetLocalOwnerName replaces display snapshots without changing the immutable
// ticket/session identity. Late authentication or registration cannot restore
// the old label. There is only one local owner per publisher instance.
func (state *Store) SetLocalOwnerName(id platform.PlayerID, at time.Time) error {
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	if id.Kind != "local" || id.PID == 0 || id.Username == "" ||
		(state.localOwnerName != nil && state.localOwnerName.PID != id.PID) {
		return errors.New("invalid local owner name snapshot")
	}
	state.localOwnerName = &id
	for conn, previous := range state.online {
		if previous.PID == id.PID {
			copy := *previous
			copy.Username = id.Username
			state.online[conn] = &copy
		}
	}
	return state.rememberName(id, at)
}
