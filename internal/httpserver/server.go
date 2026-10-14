// Package httpserver owns the existing auth and dashboard HTTP transports.
// Admission closes before draining, including requests on keep-alive sockets.
package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

type Server struct {
	server   *http.Server
	listener *ownedListener
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
	done     chan struct{}
	err      error
}

// ownedListener also makes the explicit admission stop and http.Shutdown's
// listener close idempotent. The original listener may own PROXY workers.
type ownedListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *ownedListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close() })
	return l.err
}

// Start takes ownership of an already-open listener. TLS certificates must be
// loaded into srv.TLSConfig before calling it, so startup errors are synchronous.
func Start(srv *http.Server, ln net.Listener, tls bool) *Server {
	s := &Server{server: srv, listener: &ownedListener{Listener: ln}, done: make(chan struct{})}
	handler := srv.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			w.Header().Set("Connection", "close")
			http.Error(w, "server stopping", http.StatusServiceUnavailable)
			return
		}
		s.active.Add(1)
		s.mu.Unlock()
		defer s.active.Done()
		handler.ServeHTTP(w, r)
	})
	// done is registered before the serve goroutine can finish or shutdown starts.
	go func() {
		defer close(s.done)
		if tls {
			s.err = srv.ServeTLS(s.listener, "", "")
		} else {
			s.err = srv.Serve(s.listener)
		}
	}()
	return s
}

func (s *Server) BeginShutdown() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.server.SetKeepAlivesEnabled(false)
	s.listener.Close()
}

func (s *Server) Done() <-chan struct{} { return s.done }

// Err waits for Serve and returns its original outcome. The entrypoint uses
// Done to detect unexpected termination; Wait normalizes intentional closes.
func (s *Server) Err() error { <-s.done; return s.err }

// Wait follows BeginShutdown. Its caller owns the single overall drain limit;
// no independent timeout can allow persistence to close under active handlers.
func (s *Server) Wait() error {
	err := s.server.Shutdown(context.Background())
	s.active.Wait()
	<-s.done
	if owner, ok := s.listener.Listener.(interface{ Wait() }); ok {
		owner.Wait()
	}
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	serveErr := s.err
	if errors.Is(serveErr, http.ErrServerClosed) || errors.Is(serveErr, net.ErrClosed) {
		serveErr = nil
	}
	return errors.Join(err, serveErr)
}
