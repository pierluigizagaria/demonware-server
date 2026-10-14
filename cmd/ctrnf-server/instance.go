package main

import (
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/nat"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

// serverInstance composes owners; request handlers receive their individual
// dependencies, never this orchestration object.
type serverInstance struct {
	cfg         config.ServerConfig
	database    *store.CtrSQLite
	diagnostics *diagnostics.Diagnostics

	tickets    *auth.TicketStore
	presence   *presence.PresenceStore
	ctr        *ctr.CtrServices
	matchmaker *ctr.CtrMatchmaker
	registry   *lobby.SessionRegistry
	relay      *relay.RelayTable
	nat        *nat.NatServer
	monitor    *dashboard.Telemetry
	lobby      *lobby.LobbyServer
}

func newInstance(cfg config.ServerConfig, database *store.CtrSQLite) *serverInstance {
	// Freeze the reference-valued parts as well as the scalar configuration.
	cfg.Shadnet.Scopes = slices.Clone(cfg.Shadnet.Scopes)
	cfg.News.Languages = maps.Clone(cfg.News.Languages)
	cfg.News.Fallback.Content = slices.Clone(cfg.News.Fallback.Content)
	for language, message := range cfg.News.Languages {
		message.Content = slices.Clone(message.Content)
		cfg.News.Languages[language] = message
	}
	diag := &diagnostics.Diagnostics{RedactLogs: cfg.RedactLogs, Verbose: cfg.Verbose, DumpRoot: cfg.Dumps, RunStamp: time.Now().Format("20060102-150405")}
	i := &serverInstance{cfg: cfg, database: database, diagnostics: diag}

	i.tickets = auth.NewTicketStore()
	i.presence = presence.NewPresenceStore(database)
	database.InitialBalance = func() int64 { return cfg.StartBalance }
	i.ctr = &ctr.CtrServices{Economy: &ctr.EconomyStore{Dir: cfg.DataDir, UnlockAll: cfg.UnlockAll, Backend: database, InitialBalance: database.InitialBalance}, Stats: database, News: cfg.News}
	i.matchmaker = &ctr.CtrMatchmaker{OnlinePIDs: i.presence.OnlinePIDs, Rooms: map[uint64]*ctr.CtrRoom{}}
	i.registry = lobby.NewSessionRegistry()
	i.relay = relay.NewRelayTable(diag, cfg.BindAddr, cfg.RelayPIDs)
	ids := &atomic.Uint64{}
	i.nat = &nat.NatServer{Diagnostics: diag, BindAddr: cfg.BindAddr, NextendoHost: cfg.PublicIP, ConnNum: ids, NatSeen: map[string]int{}}
	i.monitor = dashboard.NewTelemetry(cfg, i.nat.NatIntros.Load)
	replies := &lobby.ReplyCounter{}
	table, _ := ctr.NewCTRServiceTable(replies, i.ctr, i.presence, i.registry, i.matchmaker, cfg.PubfilesDir, i.relay)
	i.lobby = &lobby.LobbyServer{Table: table, Cfg: cfg, Diag: diag, Tickets: i.tickets, Presence: i.presence, Replies: replies, Ids: ids, Monitor: i.monitor, Options: &lobby.LobbyOptions{MigrateEnabled: cfg.Migrate, PublicHost: cfg.PublicIP}}
	return i
}
