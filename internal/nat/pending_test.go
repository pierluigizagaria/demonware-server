package nat

import (
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
)

func TestPendingDiscoveryActiveListenerCommitsOneExactSource(t *testing.T) {
	s := NewServer(&diagnostics.Diagnostics{RedactLogs: true}, "127.0.0.1", "127.0.0.1", &atomic.Uint64{})
	s.LocalOnly = true
	canonical := netip.MustParseAddrPort("127.64.9.8:12345")
	if err := s.SetLocalPendingEndpoint(canonical); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); s.Wait() })
	owner, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	foreign, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	probe := func(c *net.UDPConn, reply bool) {
		t.Helper()
		if _, err := c.WriteToUDP([]byte{30, 3, 0}, s.conn.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		var buf [32]byte
		n, _, err := c.ReadFromUDP(buf[:])
		if !reply {
			if failure, ok := err.(net.Error); !ok || !failure.Timeout() {
				t.Fatal("closed discovery sent a response")
			}
			return
		}
		if err != nil || n != 9 || netip.AddrFrom4([4]byte(buf[3:7])) != canonical.Addr() || binary.LittleEndian.Uint16(buf[7:9]) != canonical.Port() {
			t.Fatal("bound discovery lost canonical endpoint")
		}
	}
	probe(owner, false)
	game := owner.LocalAddr().(*net.UDPAddr).AddrPort()
	if err := s.BindLocalGameEndpoint(game); err != nil {
		t.Fatal(err)
	}
	probe(owner, true)
	probe(foreign, false)
	if s.BindLocalGameEndpoint(game) == nil || s.BindLocalGameEndpoint(foreign.LocalAddr().(*net.UDPAddr).AddrPort()) == nil {
		t.Fatal("committed source permitted rebinding")
	}
	probe(owner, true)
}

func TestPendingDiscoveryConcurrentCommitNeverReturnsPhysicalSource(t *testing.T) {
	s := &Server{LocalOnly: true}
	canonical := netip.MustParseAddrPort("127.64.9.8:12345")
	if err := s.SetLocalPendingEndpoint(canonical); err != nil {
		t.Fatal(err)
	}
	game := netip.MustParseAddrPort("127.0.0.1:3104")
	owner := net.UDPAddrFromAddrPort(game)
	foreign := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3105}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 1000 {
				out := s.localDiscoveryReply([]byte{30, 3, 0}, owner, net.IPv4(127, 0, 0, 2), 3074)
				if len(out) != 0 && (len(out) != 9 || binary.LittleEndian.Uint16(out[7:9]) != canonical.Port()) {
					t.Error("discovery exposed physical source while committing")
				}
				if s.localDiscoveryReply([]byte{30, 3, 0}, foreign, net.IPv4(127, 0, 0, 2), 3074) != nil {
					t.Error("foreign source received discovery")
				}
			}
		}()
	}
	if err := s.BindLocalGameEndpoint(game); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	s.Close()
	if s.BindLocalGameEndpoint(game) == nil {
		t.Fatal("stopped listener accepted binding")
	}
}
