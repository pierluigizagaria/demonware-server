package lobby

import (
	dashboardpkg "github.com/pierluigizagaria/demonware-server/internal/dashboard"
)

func (registry *SessionRegistry) Snapshot() []dashboardpkg.SessionSnapshot {

	registry.Mu.Lock()
	snaps := make([]dashboardpkg.SessionSnapshot, 0, len(registry.Sessions))
	for _, s := range registry.Sessions {
		snaps = append(snaps, dashboardpkg.SessionSnapshot{Id: s.Id, Owner: s.Owner, GameType: s.gameType, MaxPlayers: s.MaxPlayers, NumPlayers: s.NumPlayers})
	}
	registry.Mu.Unlock()

	return snaps
}
