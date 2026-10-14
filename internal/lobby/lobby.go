// Lobby listener. 3074 is the bd stack's historical port (shared with Xbox
// Live); the neighboring ports are also opened because the real port can't
// be deduced from the binary. The connection itself (hello, handshake,
// encrypted channel) is in handshake.go.

package lobby

import (
	"errors"
	authpkg "github.com/NextendoNetwork/nextendo-demonware/internal/auth"
	configpkg "github.com/NextendoNetwork/nextendo-demonware/internal/config"
	diagnosticspkg "github.com/NextendoNetwork/nextendo-demonware/internal/diagnostics"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
	net "net"
	strings "strings"
	"sync"
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

	mu        sync.Mutex
	stopping  bool
	listeners []net.Listener
	sessions  map[*LobbySession]struct{}
	workers   sync.WaitGroup
}

func (server *LobbyServer) NewConn(c net.Conn, dumps string) *LobbySession {
	return &LobbySession{Table: server.Table, Telemetry: server.Monitor, Diagnostics: server.Diag, Options: server.Options, ReplyCounter: server.Replies, Presence: server.Presence, Tickets: server.Tickets, C: c, N: server.Ids.Add(1), dumpDir: dumps}
}

func (server *LobbyServer) Start() error {
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
		if server.startListener(ln, dumps) {
			opened++
		}
	}
	if opened == 0 {
		return errors.New("lobby: no port could be opened")
	}
	server.Diag.DiagnosticLogf("[D3 Lobby] listening TCP %s (%d port(s), dumps=%v)", server.Cfg.LobbyPorts, opened, dumps != "")
	return nil
}

func (server *LobbyServer) startListener(ln net.Listener, dumps string) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.stopping {
		ln.Close()
		return false
	}
	server.listeners = append(server.listeners, ln)
	if server.sessions == nil {
		server.sessions = make(map[*LobbySession]struct{})
	}
	server.workers.Add(1)
	go func() {
		defer server.workers.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			server.mu.Lock()
			if server.stopping {
				server.mu.Unlock()
				c.Close()
				return
			}
			lc := server.NewConn(c, dumps)
			server.sessions[lc] = struct{}{}
			server.Monitor.RegisterConn(lc.N, c.RemoteAddr().String())
			server.workers.Add(1)
			server.mu.Unlock()
			go func() {
				defer server.workers.Done()
				defer func() {
					server.Monitor.UnregisterConn(lc.N)
					server.mu.Lock()
					delete(server.sessions, lc)
					server.mu.Unlock()
				}()
				lc.run()
			}()
		}
	}()
	return true
}

// BeginShutdown closes admission and interrupts reads, without interrupting a
// task's reply or callback. Wait must follow it before persistence is closed.
func (server *LobbyServer) BeginShutdown() {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.stopping = true
	for _, ln := range server.listeners {
		ln.Close()
	}
	for lc := range server.sessions {
		lc.RequestStop()
	}
}

func (server *LobbyServer) Wait() { server.workers.Wait() }
