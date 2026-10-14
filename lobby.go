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

type lobbyServer struct {
	cfg        serverConfig
	diag       *diagnostics
	files      *publisherFiles
	tickets    *ticketStore
	presence   *presenceStore
	replies    *replyCounter
	ctr        *ctrServices
	matchmaker *ctrMatchmaker
	registry   *sessionRegistry
	relay      *relayTable
	ids        *atomic.Uint64
	monitor    *telemetry
	options    *lobbyOptions
}

func (server *lobbyServer) newConn(c net.Conn, dumps string) *lobbyConn {
	return &lobbyConn{telemetry: server.monitor, diagnostics: server.diag, options: server.options, files: server.files, relay: server.relay, matchmaker: server.matchmaker, registry: server.registry, ctr: server.ctr, replyCounter: server.replies, presence: server.presence, tickets: server.tickets, c: c, n: server.ids.Add(1), dumpDir: dumps, pubDir: server.cfg.pubfilesDir}
}

func (server *lobbyServer) start() {
	dumps := server.diag.runDumpDir("lobby")
	opened := 0
	for _, p := range strings.Split(server.cfg.lobbyPorts, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(server.cfg.bindAddr, p))
		if err != nil {
			server.diag.diagnosticLogf("[D3 Lobby] port %s unavailable: %v", p, err)
			continue
		}
		opened++
		go func(ln net.Listener) {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				lc := server.newConn(c, dumps)
				go func() {
					server.monitor.registerConn(lc)
					defer server.monitor.unregisterConn(lc)
					lc.run()
				}()
			}
		}(ln)
	}
	if opened == 0 {
		log.Fatal("[D3 Lobby] no port could be opened")
	}
	server.diag.diagnosticLogf("[D3 Lobby] listening TCP %s (%d port(s), dumps=%v)", server.cfg.lobbyPorts, opened, dumps != "")
}
