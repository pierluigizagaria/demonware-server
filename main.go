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
	"time"
)

// Defaults; applyConfig replaces them with the validated settings (config.go).
var (
	// NEXTENDO_HOST: the server's IPv4 address as the players see it,
	// announced in NAT discovery replies.
	nextendoHost = "127.0.0.1"
	authPort     = 8460
	lobbyPorts   = "3074,3075,3076,3077,3078,3079,3080"
	natPort      = 3074
	certFile     = "cert.pem"
	keyFile      = "key.pem"
	// BIND_ADDR restricts the network listeners (default: all interfaces).
	bindAddr string

	// DATA_DIR: persistent state, ctrnf.db (economy, leaderboards, player names).
	dataDir = "data"
	// D3_PUBFILES_CONFIG drives the three files written to D3_PUBFILES.
	pubConfigPath = "pubfiles.json"
	pubDir        = "pubfiles"

	// D3_DUMPS: raw captures (auth bodies, every lobby frame) under
	// <D3_DUMPS>/<start>/. off disables them.
	dumpRoot = "off"
	// D3_VERBOSE logs every decrypted lobby message as a hex dump.
	verbose bool

	runStamp = time.Now().Format("20060102-150405")
)

// runDumpDir returns this run's capture directory for one component, "" when
// captures are off.
func runDumpDir(component string) string {
	if redactLogs || dumpRoot == "off" || dumpRoot == "0" {
		return ""
	}
	dir := filepath.Join(dumpRoot, runStamp, component)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		diagnosticLogf("[D3] dumps %s: %v", dir, err)
		return ""
	}
	return dir
}

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
	applyConfig(cfg)

	if pubfilesOnly {
		if err := writePubfiles(pubConfigPath, pubDir); err != nil {
			log.Fatalf("[D3 Pubfiles] %v", err)
		}
		return
	}

	platform, trustID, err := loadPublisherPlatform(cfg)
	if err != nil {
		log.Fatalf("[D3] publisher admission: %v", err)
	}
	log.Printf("[D3] admission: %s", cfg.admission)
	if err := writePubfiles(pubConfigPath, pubDir); err != nil {
		diagnosticLogf("[D3 Pubfiles] %v", err)
	}
	loadWallets()
	store, err := openCTRSQLite(filepath.Join(dataDir, ctrDB), cfg.unlockAll, trustID)
	if err != nil {
		log.Fatalf("[D3] CTR database: %v", err)
	}
	defer store.Close()
	economy.backend, economy.unlockAll = store, cfg.unlockAll
	ctrStats = store
	playerNames = store
	if cfg.unlockAll {
		log.Print("[D3] unlock-all: on")
	} else {
		log.Print("[D3] unlock-all: off")
	}

	go startDashboard()
	go serveNAT(natPort)
	startCTRRelay(cfg.relayLow, cfg.relayHigh, nextendoHost)
	go reapSessions()
	startLobby()

	if err := serveAuth(platform); err != nil {
		log.Fatalf("[D3 Auth] stopped: %v", err)
	}
}
