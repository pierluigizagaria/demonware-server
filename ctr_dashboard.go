package main

func (matchmaker *ctrMatchmaker) dashboardRooms(names map[uint64]string) ([]apiGathering, map[uint64]apiGathering) {
	gs := []apiGathering{}
	ctrMembership := map[uint64]apiGathering{}
	matchmaker.mu.Lock()
	for _, room := range matchmaker.rooms {
		if room.host == nil || names[room.host.pid] == "" {
			continue
		}
		g := apiGathering{ID: uint32(room.id), HostPID: room.host.pid, HostName: names[room.host.pid], Type: "Public matchmaking", Max: uint16(room.capacity), State: "searching", Players: []apiLobbyP{}}
		seen := map[uint64]bool{}
		for _, search := range room.players {
			for _, pid := range search.doc.Members {
				if seen[pid] || names[pid] == "" || (pid != room.host.pid && room.state(pid) != 2) {
					continue
				}
				seen[pid] = true
				g.Players = append(g.Players, apiLobbyP{PID: pid, Name: names[pid], Host: pid == room.host.pid})
			}
		}
		g.Count = len(g.Players)
		if g.Count > 1 {
			g.State = "matched"
		}
		if open, ok := room.hostDoc["lobby_open"].(bool); ok && !open {
			g.State = "in game"
		}
		for _, p := range g.Players {
			ctrMembership[p.PID] = g
		}
		gs = append(gs, g)
	}
	matchmaker.mu.Unlock()
	return gs, ctrMembership
}
