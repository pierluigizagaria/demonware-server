package ctr

import (
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
)

func (matchmaker *Matchmaker) DashboardRooms(names map[uint64]string) ([]dashboard.APIGathering, map[uint64]dashboard.APIGathering) {
	gs := []dashboard.APIGathering{}
	ctrMembership := map[uint64]dashboard.APIGathering{}
	matchmaker.mu.Lock()
	for _, room := range matchmaker.rooms {
		if room.Host == nil || names[room.Host.PID] == "" {
			continue
		}
		g := dashboard.APIGathering{ID: uint32(room.ID), HostPID: room.Host.PID, HostName: names[room.Host.PID], Type: "Public matchmaking", Max: uint16(room.Capacity), State: "searching", Players: []dashboard.APILobbyPlayer{}}
		seen := map[uint64]bool{}
		for _, search := range room.Players {
			for _, pid := range search.Doc.Members {
				if seen[pid] || names[pid] == "" || (pid != room.Host.PID && room.state(pid) != 2) {
					continue
				}
				seen[pid] = true
				g.Players = append(g.Players, dashboard.APILobbyPlayer{PID: pid, Name: names[pid], Host: pid == room.Host.PID})
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

// MatchmakingSnapshot observes queue order and live counts without exposing
// searches, peers, rooms, keys or maps owned by the matchmaking engine.
type MatchmakingSnapshot struct {
	WaitingIDs     []uint64
	Pending, Rooms int
}

func (m *Matchmaker) Snapshot() MatchmakingSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := MatchmakingSnapshot{Pending: len(m.pending), Rooms: len(m.rooms)}
	for _, search := range m.waiting {
		snapshot.WaitingIDs = append(snapshot.WaitingIDs, search.ID)
	}
	return snapshot
}
