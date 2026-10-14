package main

// connPID returns the Nextendo PID of a lobby connection's player (0 if unknown).
func (state *presenceStore) connPID(conn uint64) uint64 {
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	if p := state.online[conn]; p != nil {
		return p.PID
	}
	return 0
}

func (state *presenceStore) snapshot(game string) map[uint64]playerID {
	state.onlineMu.Lock()
	who := make(map[uint64]playerID, len(state.online))
	for n, p := range state.online {
		if (game == "ctr" && !isCTRTitle(p.Title)) || (game == "d3" && isCTRTitle(p.Title)) {
			continue
		}
		who[n] = *p
	}
	state.onlineMu.Unlock()

	return who
}
