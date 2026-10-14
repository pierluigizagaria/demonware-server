package ctr

import (
	configpkg "github.com/pierluigizagaria/demonware-server/internal/config"
	lobbypkg "github.com/pierluigizagaria/demonware-server/internal/lobby"
	presencepkg "github.com/pierluigizagaria/demonware-server/internal/presence"
)

type EconomyHandler struct {
	*lobbypkg.ReplyCounter
	Economy *EconomyStore
}

type StatsHandler struct {
	*lobbypkg.ReplyCounter
	Stats    LeaderboardStore
	Presence *presencepkg.PresenceStore
}

type NewsHandler struct {
	*lobbypkg.ReplyCounter
	News configpkg.CtrNewsConfig
}

type PlatformHandler struct{ *lobbypkg.ReplyCounter }
