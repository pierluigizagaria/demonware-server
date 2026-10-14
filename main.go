// Demonware server for Crash Team Racing Nitro-Fueled on PS4 (CUSA13795),
// with shadNet sign-in or explicit anonymous unlock-all admission. A fork of the Nextendo server
// for Diablo III and Crash Team Racing on Switch; the Diablo III code is kept
// unchanged but unused (FORK.md).
//
// One process serves all of it:
//
//	auth        HTTPS :AUTH_PORT (8460), reached through sni-router   auth.go platform_psn.go tickets.go
//	lobby       TCP 3074 (+3075..3080), encrypted remote tasks        lobby.go handshake.go bdcrypto.go services.go
//	matchmaking public games, friend lookups, user data               matchmaking.go friends.go
//	NAT         UDP 3074: IP / NAT discovery, introductions           nat.go
//	pubfiles    season, community events, item blacklist              pubfiles.go pubfiles.json
//	dashboard   /api/stats on DASH_PORT (8093) for nextendo-dashboard dashboard.go
//
//	server [-env file]            run the server
//	server [-env file] pubfiles   regenerate the publisher files from pubfiles.json and exit
//	                                  (the lobby reads them on every request: no restart needed)
//
// Settings are environment variables, read once and validated in config.go;
// example.env documents each one.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

func main() {
	// Logs on stdout like the other game servers (the launchers redirect it to
	// nextendo/logs/diablo-3.log).
	log.SetOutput(os.Stdout)

	envFile := flag.String("env", "", "load this instance's settings from a KEY=VALUE `file` (see example.env); variables already in the environment take precedence")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: server [-env file] [pubfiles]")
		flag.PrintDefaults()
	}
	flag.Parse()
	pubfilesOnly := flag.NArg() == 1 && flag.Arg(0) == "pubfiles"
	if flag.NArg() > 0 && !pubfilesOnly {
		flag.Usage()
		os.Exit(2)
	}
	cfg, err := readConfig(*envFile, !pubfilesOnly)
	if err != nil {
		log.Fatalf("[D3] configuration:\n%v", err)
	}
	diag := &diagnostics{redactLogs: cfg.redactLogs, verbose: cfg.verbose, dumpRoot: cfg.dumps, runStamp: time.Now().Format("20060102-150405")}
	files := &publisherFiles{diagnostics: diag, pubDir: cfg.pubfilesDir, pubConfigPath: cfg.pubfilesConfig, riftDir: cfg.riftData}

	if pubfilesOnly {
		if err := files.writePubfiles(cfg.pubfilesConfig, cfg.pubfilesDir); err != nil {
			log.Fatalf("[D3 Pubfiles] %v", err)
		}
		return
	}

	platform, trustID, err := loadPublisherPlatform(cfg)
	if err != nil {
		log.Fatalf("[D3] publisher admission: %v", err)
	}
	log.Printf("[D3] admission: %s", cfg.admission)
	if err := files.writePubfiles(cfg.pubfilesConfig, cfg.pubfilesDir); err != nil {
		diag.diagnosticLogf("[D3 Pubfiles] %v", err)
	}
	economy := &economyStore{initialBalance: func() int64 { return cfg.startBalance }}
	economy.loadWallets(cfg.dataDir)
	store, err := openCTRSQLite(filepath.Join(cfg.dataDir, ctrDB), cfg.unlockAll, trustID)
	if err != nil {
		log.Fatalf("[D3] CTR database: %v", err)
	}
	defer store.Close()
	economy.backend, economy.unlockAll = store, cfg.unlockAll
	store.initialBalance = func() int64 { return cfg.startBalance }
	ctr := &ctrServices{economy: economy, stats: store, news: cfg.news}
	presence := newPresenceStore(store)
	matchmaker := &ctrMatchmaker{onlinePIDs: presence.onlinePIDs, rooms: map[uint64]*ctrRoom{}}
	registry := newSessionRegistry()
	if cfg.unlockAll {
		log.Print("[D3] unlock-all: on")
	} else {
		log.Print("[D3] unlock-all: off")
	}

	tickets := newTicketStore()
	ids := &atomic.Uint64{}
	nat := &natServer{diagnostics: diag, bindAddr: cfg.bindAddr, nextendoHost: cfg.publicIP, connNum: ids, natSeen: map[string]int{}}
	monitor := newTelemetry(cfg, nat.natIntros.Load)
	go monitor.startDashboard(presence, matchmaker, registry, diag, files)
	go nat.serveNAT(cfg.natPort)
	relay := newRelayTable(diag, cfg.bindAddr, cfg.relayPIDs)
	relay.startCTRRelay(cfg.relayLow, cfg.relayHigh, cfg.publicIP, presence.onlinePIDs)
	go registry.reapSessions()
	startLobby(tickets, presence, &replyCounter{}, ctr, matchmaker, registry, cfg, diag, files, relay, ids, monitor)

	if err := serveAuth(platform, tickets, presence, cfg, diag, monitor); err != nil {
		log.Fatalf("[D3 Auth] stopped: %v", err)
	}
}
