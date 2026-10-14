// connPID returns the Nextendo PID of a lobby connection's player (0 if unknown).

package presence

import (
	platformpkg "github.com/pierluigizagaria/demonware-server/internal/platform"
	ps4pkg "github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
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

func (state *PresenceStore) Snapshot(game string) map[uint64]platformpkg.PlayerID {
	state.OnlineMu.Lock()
	who := make(map[uint64]platformpkg.PlayerID, len(state.Online))
	for n, p := range state.Online {
		if (game == "ctr" && !ps4pkg.IsCTRTitle(p.Title)) || (game == "d3" && ps4pkg.IsCTRTitle(p.Title)) {
			continue
		}
		who[n] = *p
	}
	state.OnlineMu.Unlock()

	return who
}

func (state *PresenceStore) RegisterOnline(conn uint64, id *platformpkg.PlayerID) {
	state.OnlineMu.Lock()
	state.Online[conn] = id
	state.OnlineMu.Unlock()
}

func (state *PresenceStore) ConnectionPlayer(conn uint64) *platformpkg.PlayerID {
	state.OnlineMu.Lock()
	defer state.OnlineMu.Unlock()
	if p := state.Online[conn]; p != nil {
		id := *p
		return &id
	}
	return nil
}
