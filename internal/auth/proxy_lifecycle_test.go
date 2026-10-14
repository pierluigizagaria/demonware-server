package auth

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type pipeListener struct {
	conn      net.Conn
	once      sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.conn })
	if c != nil {
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *pipeListener) Close() error   { l.closeOnce.Do(func() { close(l.closed) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestProxyShutdownJoinsPendingHeadersAndHandoffs(t *testing.T) {
	for _, header := range []string{"PR", "PROXY UNKNOWN\r\n"} {
		t.Run(map[bool]string{true: "header", false: "handoff"}[header == "PR"], func(t *testing.T) {
			server, client := net.Pipe()
			defer client.Close()
			listener := NewProxyListener(&pipeListener{conn: server, closed: make(chan struct{})})
			defer listener.Close()
			if _, err := io.WriteString(client, header); err != nil {
				t.Fatal(err)
			}
			listener.Close()
			done := make(chan struct{})
			go func() { listener.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("proxy worker remained blocked")
			}
			if _, err := listener.Accept(); err == nil {
				t.Fatal("closed listener admitted a connection")
			}
		})
	}
}

func TestProxyShutdownLeavesAcceptedConnectionToHTTP(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	listener := NewProxyListener(&pipeListener{conn: server, closed: make(chan struct{})})
	defer listener.Close()
	written := make(chan error, 1)
	go func() { _, err := io.WriteString(client, "ABCDEF"); written <- err }()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	listener.Close()
	listener.Wait()
	b := make([]byte, 6)
	if _, err := io.ReadFull(conn, b); err != nil || string(b) != "ABCDEF" {
		t.Fatal("accepted connection closed by proxy owner")
	}
}
