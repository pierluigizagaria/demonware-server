package presence

import (
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

// ConnPID returns the Nextendo PID of a lobby connection's player (0 if unknown).
func (state *Store) ConnPID(conn uint64) uint64 {
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	if p := state.online[conn]; p != nil {
		return p.PID
	}
	return 0
}

// Snapshot observes copied online identities, filtered by title.
func (state *Store) Snapshot(game string) map[uint64]platform.PlayerID {
	state.onlineMu.Lock()
	who := make(map[uint64]platform.PlayerID, len(state.online))
	for n, p := range state.online {
		if (game == "ctr" && !ps4.IsCTRTitle(p.Title)) || (game == "d3" && ps4.IsCTRTitle(p.Title)) {
			continue
		}
		who[n] = *p
	}
	state.onlineMu.Unlock()

	return who
}

func (state *Store) RegisterOnline(conn uint64, id *platform.PlayerID) {
	state.onlineMu.Lock()
	identity := *id
	if current := state.localOwnerName; current != nil && current.PID == identity.PID {
		identity.Username = current.Username
	}
	state.online[conn] = &identity
	state.onlineMu.Unlock()
}

func (state *Store) ConnectionPlayer(conn uint64) *platform.PlayerID {
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	if p := state.online[conn]; p != nil {
		id := *p
		return &id
	}
	return nil
}

// Counts is an observation of the separately locked presence stores. No live
// maps, identities or rich-presence payloads escape through this view.
type Counts struct{ Online, UserData, RichPresence int }

func (state *Store) Counts() Counts {
	var counts Counts
	state.onlineMu.Lock()
	counts.Online = len(state.online)
	state.onlineMu.Unlock()
	state.userDataMu.Lock()
	counts.UserData = len(state.userData)
	state.userDataMu.Unlock()
	state.richMu.Lock()
	counts.RichPresence = len(state.rich)
	state.richMu.Unlock()
	return counts
}
