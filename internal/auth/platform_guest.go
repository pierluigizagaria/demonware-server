package auth

import (
	platformpkg "github.com/pierluigizagaria/demonware-server/internal/platform"
)

// Admission is selected once per instance, never as a verification fallback.
type PublisherPlatform interface {
	Authenticate([]byte) (platformpkg.PlayerID, error)
	AuthData(platformpkg.PlayerID) map[string]string
}
