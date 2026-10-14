// connPID returns the Nextendo PID of a lobby connection's player (0 if unknown).

package presence

import (
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

// connPID returns the Nextendo PID of a lobby connection's player (0 if unknown).
func (state *PresenceStore) ConnPID(conn uint64) uint64 {
	state.OnlineMu.Lock()
	defer state.OnlineMu.Unlock()
	if p := state.Online[conn]; p != nil {
		return p.PID
	}
	return 0
}

func (state *PresenceStore) Snapshot(game string) map[uint64]platform.PlayerID {
	state.OnlineMu.Lock()
	who := make(map[uint64]platform.PlayerID, len(state.Online))
	for n, p := range state.Online {
		if (game == "ctr" && !ps4.IsCTRTitle(p.Title)) || (game == "d3" && ps4.IsCTRTitle(p.Title)) {
			continue
		}
		who[n] = *p
	}
	state.OnlineMu.Unlock()

	return who
}

func (state *PresenceStore) RegisterOnline(conn uint64, id *platform.PlayerID) {
	state.OnlineMu.Lock()
	state.Online[conn] = id
	state.OnlineMu.Unlock()
}

func (state *PresenceStore) ConnectionPlayer(conn uint64) *platform.PlayerID {
	state.OnlineMu.Lock()
	defer state.OnlineMu.Unlock()
	if p := state.Online[conn]; p != nil {
		id := *p
		return &id
	}
	return nil
}
