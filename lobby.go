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
	table *serviceTable
	cfg   serverConfig
	diag  *diagnostics

	tickets  *ticketStore
	presence *presenceStore
	replies  *replyCounter

	ids     *atomic.Uint64
	monitor sessionTelemetry
	options *lobbyOptions
}

func (server *lobbyServer) newConn(c net.Conn, dumps string) *lobbySession {
	return &lobbySession{table: server.table, telemetry: server.monitor, diagnostics: server.diag, options: server.options, replyCounter: server.replies, presence: server.presence, tickets: server.tickets, c: c, n: server.ids.Add(1), dumpDir: dumps}
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
					server.monitor.registerConn(lc.n, lc.c.RemoteAddr().String())
					defer server.monitor.unregisterConn(lc.n)
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
