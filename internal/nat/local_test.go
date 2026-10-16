package nat

import (
	"bytes"
	"net"
	"net/netip"
	"testing"
)

func TestLocalDiscoveryExactShapes(t *testing.T) {
	source := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 6), Port: 3074}
	server := net.IPv4(127, 0, 0, 5)
	for _, test := range []struct{ in, out []byte }{
		{[]byte{30, 3, 0}, []byte{31, 2, 0, 127, 0, 0, 6, 2, 12}},
		{[]byte{20, 2, 0, 0}, []byte{21, 2, 0, 127, 0, 0, 6, 2, 12, 127, 0, 0, 5, 2, 12}},
		{[]byte{20, 2, 0, 2}, nil}, {[]byte{20, 2, 0, 3}, nil}, {append([]byte{14, 2, 0}, make([]byte, 26)...), nil}, {append([]byte{10, 2, 0}, make([]byte, 26)...), nil}, {[]byte{30, 3, 0, 0}, nil},
	} {
		if got := localDiscoveryReply(test.in, source, server, 3074); !bytes.Equal(got, test.out) {
			t.Fatal("unexpected discovery reply")
		}
	}
	source.IP = net.IPv4(192, 168, 1, 1)
	if localDiscoveryReply([]byte{30, 3, 0}, source, server, 3074) != nil {
		t.Fatal("nonlocal source accepted")
	}
}

func TestVirtualDiscoveryOnlyForExactGameSource(t *testing.T) {
	s := &Server{LocalOnly: true}
	game := netip.MustParseAddrPort("127.0.0.6:3074")
	canonical := netip.MustParseAddrPort("198.19.9.8:12345")
	if err := s.SetLocalVirtualEndpoint(game, canonical); err != nil {
		t.Fatal(err)
	}
	server := net.IPv4(127, 0, 0, 5)
	for _, ip := range []net.IP{net.IPv4(127, 0, 0, 6), net.IP{127, 0, 0, 6}} {
		source := &net.UDPAddr{IP: ip, Port: 3074}
		for _, packet := range [][]byte{{30, 3, 0}, {20, 2, 0, 0}} {
			got := s.localDiscoveryReply(packet, source, server, 3074)
			want := localDiscoveryReply(packet, source, server, 3074)
			copy(want[3:9], []byte{198, 19, 9, 8, 57, 48})
			if !bytes.Equal(got, want) {
				t.Fatal("canonical observed endpoint or physical alternate differs")
			}
		}
		for _, packet := range [][]byte{{20, 2, 0, 2}, {20, 2, 0, 3}, {30, 3, 0, 0}, append([]byte{10, 2, 0}, make([]byte, 26)...)} {
			if s.localDiscoveryReply(packet, source, server, 3074) != nil {
				t.Fatal("unsupported packet gained a reply")
			}
		}
	}
	for _, source := range []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 7), Port: 3074}, {IP: net.IPv4(127, 0, 0, 6), Port: 3075}, {IP: net.IPv4(192, 0, 2, 1), Port: 3074}} {
		packet := []byte{30, 3, 0}
		if !bytes.Equal(s.localDiscoveryReply(packet, source, server, 3074), localDiscoveryReply(packet, source, server, 3074)) {
			t.Fatal("foreign source received canonical endpoint")
		}
	}
	legacy := &Server{}
	if legacy.SetLocalVirtualEndpoint(game, canonical) == nil {
		t.Fatal("central mode accepted virtual discovery")
	}
}
