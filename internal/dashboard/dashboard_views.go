// The monitoring consumer receives snapshots and never locks its providers.

package dashboard

import (
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
)

// The monitoring consumer receives snapshots and never locks its providers.
type SessionSnapshot struct {
	Id                               [8]byte
	Owner                            uint64
	GameType, MaxPlayers, NumPlayers uint32
}

type DashboardViews struct {
	Online   func(string) map[uint64]platformpkg.PlayerID
	Sessions func() []SessionSnapshot
	Rooms    func(map[uint64]string) ([]ApiGathering, map[uint64]ApiGathering)
}
