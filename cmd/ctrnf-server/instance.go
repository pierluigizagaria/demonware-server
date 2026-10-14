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
	cfg         config.Config
	database    *store.SQLite
	diagnostics *diagnostics.Diagnostics

	tickets    *auth.TicketStore
	presence   *presence.Store
	ctr        *ctr.Services
	matchmaker *ctr.Matchmaker
	registry   *lobby.SessionRegistry
	relay      *relay.Table
	nat        *nat.Server
	monitor    *dashboard.Telemetry
	lobby      *lobby.Server
}

func newInstance(cfg config.Config, database *store.SQLite) *serverInstance {
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
	i.presence = presence.NewStore(database)
	database.InitialBalance = func() int64 { return cfg.StartBalance }
	i.ctr = &ctr.Services{Economy: &ctr.EconomyStore{Dir: cfg.DataDir, UnlockAll: cfg.UnlockAll, Backend: database, InitialBalance: database.InitialBalance}, Stats: database, News: cfg.News}
	i.matchmaker = ctr.NewMatchmaker(i.presence.OnlinePIDs)
	i.registry = lobby.NewSessionRegistry()
	i.relay = relay.NewTable(diag, cfg.BindAddr, cfg.RelayPIDs)
	ids := &atomic.Uint64{}
	i.nat = nat.NewServer(diag, cfg.BindAddr, cfg.PublicIP, ids)
	i.monitor = dashboard.NewTelemetry(cfg, i.nat.Introductions)
	replies := &lobby.ReplyCounter{}
	table, _ := ctr.NewServiceTable(replies, i.ctr, i.presence, i.registry, i.matchmaker, cfg.PubfilesDir, i.relay)
	i.lobby = lobby.NewServer(cfg, table, diag, i.tickets, i.presence, replies, ids, i.monitor, &lobby.Options{MigrateEnabled: cfg.Migrate, PublicHost: cfg.PublicIP})
	return i
}
