package ctr

import (
	configpkg "github.com/NextendoNetwork/nextendo-demonware/internal/config"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
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
