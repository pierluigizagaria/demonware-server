package lobby

import (
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
)

// Snapshot observes copied registry metadata for monitoring and lifecycle checks.
func (registry *SessionRegistry) Snapshot() []dashboard.SessionSnapshot {

	registry.mu.Lock()
	snaps := make([]dashboard.SessionSnapshot, 0, len(registry.sessions))
	for _, s := range registry.sessions {
		snaps = append(snaps, dashboard.SessionSnapshot{Context: s.Context, OwnerPID: s.OwnerPID, Reserved: s.Reserved, Updated: s.Updated, ID: s.ID, Owner: s.Owner, GameType: s.gameType, MaxPlayers: s.MaxPlayers, NumPlayers: s.NumPlayers})
	}
	registry.mu.Unlock()

	return snaps
}
