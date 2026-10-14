// Lobby listener. 3074 is the bd stack's historical port (shared with Xbox
// Live); the neighboring ports are also opened because the real port can't
// be deduced from the binary. The connection itself (hello, handshake,
// encrypted channel) is in handshake.go.

package lobby

import (
	authpkg "github.com/NextendoNetwork/nextendo-demonware/internal/auth"
	configpkg "github.com/NextendoNetwork/nextendo-demonware/internal/config"
	diagnosticspkg "github.com/NextendoNetwork/nextendo-demonware/internal/diagnostics"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
	log "log"
	net "net"
	strings "strings"
	atomic "sync/atomic"
)

type LobbyServer struct {
	Table *ServiceTable
	Cfg   configpkg.ServerConfig
	Diag  *diagnosticspkg.Diagnostics

	Tickets  *authpkg.TicketStore
	Presence *presencepkg.PresenceStore
	Replies  *ReplyCounter

	Ids     *atomic.Uint64
	Monitor sessionTelemetry
	Options *LobbyOptions
}

func (server *LobbyServer) NewConn(c net.Conn, dumps string) *LobbySession {
	return &LobbySession{Table: server.Table, Telemetry: server.Monitor, Diagnostics: server.Diag, Options: server.Options, ReplyCounter: server.Replies, Presence: server.Presence, Tickets: server.Tickets, C: c, N: server.Ids.Add(1), dumpDir: dumps}
}

func (server *LobbyServer) Start() {
	dumps := server.Diag.RunDumpDir("lobby")
	opened := 0
	for _, p := range strings.Split(server.Cfg.LobbyPorts, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(server.Cfg.BindAddr, p))
		if err != nil {
			server.Diag.DiagnosticLogf("[D3 Lobby] port %s unavailable: %v", p, err)
			continue
		}
		opened++
		go func(ln net.Listener) {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				lc := server.NewConn(c, dumps)
				go func() {
					server.Monitor.RegisterConn(lc.N, lc.C.RemoteAddr().String())
					defer server.Monitor.UnregisterConn(lc.N)
					lc.run()
				}()
			}
		}(ln)
	}
	if opened == 0 {
		log.Fatal("[D3 Lobby] no port could be opened")
	}
	server.Diag.DiagnosticLogf("[D3 Lobby] listening TCP %s (%d port(s), dumps=%v)", server.Cfg.LobbyPorts, opened, dumps != "")
}
