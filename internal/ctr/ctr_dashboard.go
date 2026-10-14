package ctr

import (
	dashboardpkg "github.com/pierluigizagaria/demonware-server/internal/dashboard"
)

func (matchmaker *CtrMatchmaker) DashboardRooms(names map[uint64]string) ([]dashboardpkg.ApiGathering, map[uint64]dashboardpkg.ApiGathering) {
	gs := []dashboardpkg.ApiGathering{}
	ctrMembership := map[uint64]dashboardpkg.ApiGathering{}
	matchmaker.mu.Lock()
	for _, room := range matchmaker.Rooms {
		if room.Host == nil || names[room.Host.Pid] == "" {
			continue
		}
		g := dashboardpkg.ApiGathering{ID: uint32(room.Id), HostPID: room.Host.Pid, HostName: names[room.Host.Pid], Type: "Public matchmaking", Max: uint16(room.Capacity), State: "searching", Players: []dashboardpkg.ApiLobbyP{}}
		seen := map[uint64]bool{}
		for _, search := range room.Players {
			for _, pid := range search.Doc.Members {
				if seen[pid] || names[pid] == "" || (pid != room.Host.Pid && room.state(pid) != 2) {
					continue
				}
				seen[pid] = true
				g.Players = append(g.Players, dashboardpkg.ApiLobbyP{PID: pid, Name: names[pid], Host: pid == room.Host.Pid})
			}
		}
		g.Count = len(g.Players)
		if g.Count > 1 {
			g.State = "matched"
		}
		if open, ok := room.HostDoc["lobby_open"].(bool); ok && !open {
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
