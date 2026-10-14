// Lobby listener. 3074 is the bd stack's historical port (shared with Xbox
// Live); the neighboring ports are also opened because the real port can't
// be deduced from the binary. The connection itself (hello, handshake,
// encrypted channel) is in handshake.go.

package lobby

import (
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

type Server struct {
	Table *ServiceTable
	Cfg   config.Config
	Diag  *diagnostics.Diagnostics

	Tickets  *auth.TicketStore
	Presence *presence.Store
	Replies  *ReplyCounter

	ids     *atomic.Uint64
	Monitor sessionTelemetry
	Options *Options

	mu        sync.Mutex
	stopping  bool
	listeners []net.Listener
	sessions  map[*Session]struct{}
	workers   sync.WaitGroup
}

// NewServer wires one instance without opening listeners or starting workers.
// ids and options are shared pointers; their atomics are never copied.
func NewServer(cfg config.Config, table *ServiceTable, diag *diagnostics.Diagnostics, tickets *auth.TicketStore, presenceStore *presence.Store, replies *ReplyCounter, ids *atomic.Uint64, monitor sessionTelemetry, options *Options) *Server {
	return &Server{Cfg: cfg, Table: table, Diag: diag, Tickets: tickets, Presence: presenceStore, Replies: replies, ids: ids, Monitor: monitor, Options: options, sessions: map[*Session]struct{}{}}
}

func (server *Server) NewConn(c net.Conn, dumps string) *Session {
	return &Session{Table: server.Table, Telemetry: server.Monitor, Diagnostics: server.Diag, Options: server.Options, ReplyCounter: server.Replies, Presence: server.Presence, Tickets: server.Tickets, conn: c, N: server.ids.Add(1), dumpDir: dumps}
}

func (server *Server) Start() error {
	dumps := server.Diag.RunDumpDir("lobby")
	opened := 0
	for _, p := range strings.Split(server.Cfg.LobbyPorts, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(server.Cfg.BindAddr, p))
		if err != nil {
			server.Diag.DiagnosticLogf("[lobby] port %s unavailable: %v", p, err)
			continue
		}
		if server.startListener(ln, dumps) {
			opened++
		}
	}
	if opened == 0 {
		return errors.New("lobby: no port could be opened")
	}
	server.Diag.DiagnosticLogf("[lobby] listening TCP %s (%d port(s), dumps=%v)", server.Cfg.LobbyPorts, opened, dumps != "")
	return nil
}

func (server *Server) startListener(ln net.Listener, dumps string) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.stopping {
		ln.Close()
		return false
	}
	server.listeners = append(server.listeners, ln)
	if server.sessions == nil {
		server.sessions = make(map[*Session]struct{})
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
func (server *Server) BeginShutdown() {
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

func (server *Server) Wait() { server.workers.Wait() }
