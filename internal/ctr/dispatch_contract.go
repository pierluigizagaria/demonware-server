package ctr

import (
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

type EconomyHandler struct {
	*lobby.ReplyCounter
	Economy *EconomyStore
}

type StatsHandler struct {
	*lobby.ReplyCounter
	Stats    LeaderboardStore
	Presence *presence.PresenceStore
}

type NewsHandler struct {
	*lobby.ReplyCounter
	News config.CtrNewsConfig
}

type PlatformHandler struct{ *lobby.ReplyCounter }
