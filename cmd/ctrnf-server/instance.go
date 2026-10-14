package main

import (
	authpkg "github.com/pierluigizagaria/demonware-server/internal/auth"
	configpkg "github.com/pierluigizagaria/demonware-server/internal/config"
	ctrpkg "github.com/pierluigizagaria/demonware-server/internal/ctr"
	dashboardpkg "github.com/pierluigizagaria/demonware-server/internal/dashboard"
	diagnosticspkg "github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	lobbypkg "github.com/pierluigizagaria/demonware-server/internal/lobby"
	natpkg "github.com/pierluigizagaria/demonware-server/internal/nat"
	presencepkg "github.com/pierluigizagaria/demonware-server/internal/presence"
	relaypkg "github.com/pierluigizagaria/demonware-server/internal/relay"
	storepkg "github.com/pierluigizagaria/demonware-server/internal/store"
	maps "maps"
	slices "slices"
	atomic "sync/atomic"
	time "time"
)

// serverInstance composes owners; request handlers receive their individual
// dependencies, never this orchestration object.
type serverInstance struct {
	cfg         configpkg.ServerConfig
	database    *storepkg.CtrSQLite
	diagnostics *diagnosticspkg.Diagnostics

	tickets    *authpkg.TicketStore
	presence   *presencepkg.PresenceStore
	ctr        *ctrpkg.CtrServices
	matchmaker *ctrpkg.CtrMatchmaker
	registry   *lobbypkg.SessionRegistry
	relay      *relaypkg.RelayTable
	nat        *natpkg.NatServer
	monitor    *dashboardpkg.Telemetry
	lobby      *lobbypkg.LobbyServer
}

func newInstance(cfg configpkg.ServerConfig, database *storepkg.CtrSQLite) *serverInstance {
	// Freeze the reference-valued parts as well as the scalar configuration.
	cfg.Shadnet.Scopes = slices.Clone(cfg.Shadnet.Scopes)
	cfg.News.Languages = maps.Clone(cfg.News.Languages)
	cfg.News.Fallback.Content = slices.Clone(cfg.News.Fallback.Content)
	for language, message := range cfg.News.Languages {
		message.Content = slices.Clone(message.Content)
		cfg.News.Languages[language] = message
	}
	diag := &diagnosticspkg.Diagnostics{RedactLogs: cfg.RedactLogs, Verbose: cfg.Verbose, DumpRoot: cfg.Dumps, RunStamp: time.Now().Format("20060102-150405")}
	i := &serverInstance{cfg: cfg, database: database, diagnostics: diag}

	i.tickets = authpkg.NewTicketStore()
	i.presence = presencepkg.NewPresenceStore(database)
	database.InitialBalance = func() int64 { return cfg.StartBalance }
	i.ctr = &ctrpkg.CtrServices{Economy: &ctrpkg.EconomyStore{Dir: cfg.DataDir, UnlockAll: cfg.UnlockAll, Backend: database, InitialBalance: database.InitialBalance}, Stats: database, News: cfg.News}
	i.matchmaker = &ctrpkg.CtrMatchmaker{OnlinePIDs: i.presence.OnlinePIDs, Rooms: map[uint64]*ctrpkg.CtrRoom{}}
	i.registry = lobbypkg.NewSessionRegistry()
	i.relay = relaypkg.NewRelayTable(diag, cfg.BindAddr, cfg.RelayPIDs)
	ids := &atomic.Uint64{}
	i.nat = &natpkg.NatServer{Diagnostics: diag, BindAddr: cfg.BindAddr, NextendoHost: cfg.PublicIP, ConnNum: ids, NatSeen: map[string]int{}}
	i.monitor = dashboardpkg.NewTelemetry(cfg, i.nat.NatIntros.Load)
	replies := &lobbypkg.ReplyCounter{}
	table, _ := ctrpkg.NewCTRServiceTable(replies, i.ctr, i.presence, i.registry, i.matchmaker, cfg.PubfilesDir, i.relay)
	i.lobby = &lobbypkg.LobbyServer{Table: table, Cfg: cfg, Diag: diag, Tickets: i.tickets, Presence: i.presence, Replies: replies, Ids: ids, Monitor: i.monitor, Options: &lobbypkg.LobbyOptions{MigrateEnabled: cfg.Migrate, PublicHost: cfg.PublicIP}}
	return i
}
