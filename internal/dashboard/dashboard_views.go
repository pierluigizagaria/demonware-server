// The monitoring consumer receives snapshots and never locks its providers.

package dashboard

import (
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

// The monitoring consumer receives snapshots and never locks its providers.
type SessionSnapshot struct {
	ID                               [8]byte
	Owner                            uint64
	GameType, MaxPlayers, NumPlayers uint32
}

type Views struct {
	Online   func(string) map[uint64]platform.PlayerID
	Sessions func() []SessionSnapshot
	Rooms    func(map[uint64]string) ([]APIGathering, map[uint64]APIGathering)
}
