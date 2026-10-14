package nat

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
)

type failedPacket struct {
	reads  atomic.Int64
	closed atomic.Bool
}

func (p *failedPacket) ReadFrom([]byte) (int, net.Addr, error) {
	p.reads.Add(1)
	return 0, nil, errors.New("synthetic read failure")
}
func (p *failedPacket) WriteTo([]byte, net.Addr) (int, error) { return 0, nil }
func (p *failedPacket) Close() error                          { p.closed.Store(true); return nil }
func (p *failedPacket) LocalAddr() net.Addr                   { return &net.UDPAddr{} }
func (p *failedPacket) SetDeadline(time.Time) error           { return nil }
func (p *failedPacket) SetReadDeadline(time.Time) error       { return nil }
func (p *failedPacket) SetWriteDeadline(time.Time) error      { return nil }
func TestNATReadErrorStopsWorker(t *testing.T) {
	p := &failedPacket{}
	s := &NatServer{Diagnostics: &diagnostics.Diagnostics{RedactLogs: true}, NextendoHost: "127.0.0.1"}
	if err := s.startPacket(p, 3074); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		s.Close()
		t.Fatal("NAT spun on a read error")
	}
	if p.reads.Load() != 1 || !p.closed.Load() {
		t.Fatal("failed NAT socket not closed once the read failed")
	}
	s.Close()
	s.Close()
	if err := s.startPacket(&failedPacket{}, 3074); err == nil {
		t.Fatal("NAT restarted after shutdown")
	}
}
