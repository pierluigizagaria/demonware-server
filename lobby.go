package main

// Lobby listener. 3074 is the bd stack's historical port (shared with Xbox
// Live); the neighboring ports are also opened because the real port can't
// be deduced from the binary. The connection itself (hello, handshake,
// encrypted channel) is in handshake.go.

import (
	"log"
	"net"
	"strings"
	"sync/atomic"
)

func startLobby(tickets *ticketStore, presence *presenceStore, replies *replyCounter, ctr *ctrServices, matchmaker *ctrMatchmaker, registry *sessionRegistry, cfg serverConfig, diag *diagnostics, files *publisherFiles, relay *relayTable, connNum *atomic.Uint64, monitor *telemetry) {
	options := &lobbyOptions{migrateEnabled: cfg.migrate, publicHost: cfg.publicIP}
	dumps := diag.runDumpDir("lobby")
	opened := 0
	for _, p := range strings.Split(cfg.lobbyPorts, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(cfg.bindAddr, p))
		if err != nil {
			diag.diagnosticLogf("[D3 Lobby] port %s unavailable: %v", p, err)
			continue
		}
		opened++
		go func(ln net.Listener) {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				lc := &lobbyConn{telemetry: monitor, diagnostics: diag, options: options, files: files, relay: relay, matchmaker: matchmaker, registry: registry, ctr: ctr, replyCounter: replies, presence: presence, tickets: tickets, c: c, n: connNum.Add(1), dumpDir: dumps, pubDir: cfg.pubfilesDir}
				go func() {
					monitor.registerConn(lc)
					defer monitor.unregisterConn(lc)
					lc.run()
				}()
			}
		}(ln)
	}
	if opened == 0 {
		log.Fatal("[D3 Lobby] no port could be opened")
	}
	diag.diagnosticLogf("[D3 Lobby] listening TCP %s (%d port(s), dumps=%v)", cfg.lobbyPorts, opened, dumps != "")
}
