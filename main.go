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
	if pubfilesOnly {
		log.Print("[diablo] pubfiles is inactive in this CTR:NF server; no files generated")
		return
	}

	platform, trustID, err := loadPublisherPlatform(cfg)
	if err != nil {
		log.Fatalf("[D3] publisher admission: %v", err)
	}
	log.Printf("[D3] admission: %s", cfg.admission)
	if err := os.MkdirAll(cfg.dataDir, 0755); err != nil {
		panic(err)
	}
	store, err := openCTRSQLite(filepath.Join(cfg.dataDir, ctrDB), cfg.unlockAll, trustID)
	if err != nil {
		log.Fatalf("[D3] CTR database: %v", err)
	}
	defer store.Close()
	instance := newInstance(cfg, store)
	if cfg.unlockAll {
		log.Print("[D3] unlock-all: on")
	} else {
		log.Print("[D3] unlock-all: off")
	}

	go instance.monitor.startDashboard(newDashboardViews(instance.presence, instance.matchmaker, instance.registry), instance.diagnostics)
	go instance.nat.serveNAT(cfg.natPort)
	instance.relay.startCTRRelay(cfg.relayLow, cfg.relayHigh, cfg.publicIP, instance.presence.onlinePIDs)
	go instance.registry.reapSessions()
	instance.lobby.start()

	if err := serveAuth(platform, instance.tickets, instance.presence, cfg, instance.diagnostics, instance.monitor, newUmbrellaHandler(instance.diagnostics, instance.tickets)); err != nil {
		log.Fatalf("[D3 Auth] stopped: %v", err)
	}
}
