package httpserver

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testListener struct {
	closed chan struct{}
	once   sync.Once
	joined atomic.Bool
}

func (l *testListener) Accept() (net.Conn, error) { <-l.closed; return nil, net.ErrClosed }
func (l *testListener) Close() error              { l.once.Do(func() { close(l.closed) }); return nil }
func (*testListener) Addr() net.Addr              { return &net.TCPAddr{} }
func (l *testListener) Wait()                     { l.joined.Store(true) }

func TestShutdownRejectsNewRequestsAndDrainsHandler(t *testing.T) {
	ln := &testListener{closed: make(chan struct{})}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		w.WriteHeader(http.StatusCreated)
	})}
	s := Start(srv, ln, false)
	first := httptest.NewRecorder()
	go func() { srv.Handler.ServeHTTP(first, httptest.NewRequest("POST", "/auth/", nil)); close(finished) }()
	<-entered
	s.BeginShutdown()
	second := httptest.NewRecorder()
	srv.Handler.ServeHTTP(second, httptest.NewRequest("POST", "/auth/", nil))
	if second.Code != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatal("new request reached application during shutdown")
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("returned under active handler: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP owner did not drain")
	}
	<-finished
	if first.Code != http.StatusCreated || !ln.joined.Load() {
		t.Fatal("handler response or listener workers not completed")
	}
}

func TestShutdownBeforeServeStartsIsJoined(t *testing.T) {
	for range 20 {
		ln := &testListener{closed: make(chan struct{})}
		s := Start(&http.Server{Handler: http.NotFoundHandler()}, ln, false)
		s.BeginShutdown()
		if err := s.Wait(); err != nil {
			t.Fatal(err)
		}
		if !ln.joined.Load() {
			t.Fatal("listener workers not joined")
		}
	}
}
