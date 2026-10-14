package relay

import (
	"net"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
)

func TestRelayShutdownJoinsReaperAndSocket(t *testing.T) {
	r := NewRelayTable(&diagnostics.Diagnostics{RedactLogs: true}, "127.0.0.1", "*")
	r.StartCTRRelay(20000, 20001, "127.0.0.1", func() []uint64 { return nil })
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	pair := &RelayPair{table: r, Conn: conn}
	r.mu.Lock()
	r.Pairs["fixture"] = pair
	r.workers.Add(1)
	r.mu.Unlock()
	go func() { defer r.workers.Done(); pair.serve() }()
	r.Close()
	r.Close()
	done := make(chan struct{})
	go func() { r.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay worker did not stop")
	}
	if r.pairFor(1, 2, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 99}) != nil {
		t.Fatal("relay allocated after shutdown")
	}
}
