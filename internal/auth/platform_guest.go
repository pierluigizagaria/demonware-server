package auth

import (
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

// Admission is selected once per instance, never as a verification fallback.
type PublisherPlatform interface {
	Authenticate([]byte) (platform.PlayerID, error)
	AuthData(platform.PlayerID) map[string]string
}
