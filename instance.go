package main

import (
	"maps"
	"slices"
	"sync/atomic"
	"time"
)

// serverInstance composes owners; request handlers receive their individual
// dependencies, never this orchestration object.
type serverInstance struct {
	cfg         serverConfig
	database    *ctrSQLite
	diagnostics *diagnostics
	files       *publisherFiles
	tickets     *ticketStore
	presence    *presenceStore
	ctr         *ctrServices
	matchmaker  *ctrMatchmaker
	registry    *sessionRegistry
	relay       *relayTable
	nat         *natServer
	monitor     *telemetry
	lobby       *lobbyServer
}

func newInstance(cfg serverConfig, database *ctrSQLite) *serverInstance {
	// Freeze the reference-valued parts as well as the scalar configuration.
	cfg.shadnet.Scopes = slices.Clone(cfg.shadnet.Scopes)
	cfg.news.languages = maps.Clone(cfg.news.languages)
	cfg.news.fallback.content = slices.Clone(cfg.news.fallback.content)
	for language, message := range cfg.news.languages {
		message.content = slices.Clone(message.content)
		cfg.news.languages[language] = message
	}
	diag := &diagnostics{redactLogs: cfg.redactLogs, verbose: cfg.verbose, dumpRoot: cfg.dumps, runStamp: time.Now().Format("20060102-150405")}
	i := &serverInstance{cfg: cfg, database: database, diagnostics: diag}
	i.files = &publisherFiles{diagnostics: diag, pubDir: cfg.pubfilesDir, pubConfigPath: cfg.pubfilesConfig, riftDir: cfg.riftData}
	i.tickets = newTicketStore()
	i.presence = newPresenceStore(database)
	database.initialBalance = func() int64 { return cfg.startBalance }
	i.ctr = &ctrServices{economy: &economyStore{dir: cfg.dataDir, unlockAll: cfg.unlockAll, backend: database, initialBalance: database.initialBalance}, stats: database, news: cfg.news}
	i.matchmaker = &ctrMatchmaker{onlinePIDs: i.presence.onlinePIDs, rooms: map[uint64]*ctrRoom{}}
	i.registry = newSessionRegistry()
	i.relay = newRelayTable(diag, cfg.bindAddr, cfg.relayPIDs)
	ids := &atomic.Uint64{}
	i.nat = &natServer{diagnostics: diag, bindAddr: cfg.bindAddr, nextendoHost: cfg.publicIP, connNum: ids, natSeen: map[string]int{}}
	i.monitor = newTelemetry(cfg, i.nat.natIntros.Load)
	replies := &replyCounter{}
	table, _ := newCTRServiceTable(replies, i.ctr, i.presence, i.registry, i.matchmaker, i.files, cfg.pubfilesDir, i.relay)
	i.lobby = &lobbyServer{table: table, cfg: cfg, diag: diag, files: i.files, tickets: i.tickets, presence: i.presence, replies: replies, ctr: i.ctr, matchmaker: i.matchmaker, registry: i.registry, relay: i.relay, ids: ids, monitor: i.monitor, options: &lobbyOptions{migrateEnabled: cfg.migrate, publicHost: cfg.publicIP}}
	return i
}
