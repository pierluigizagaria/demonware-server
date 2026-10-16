package nat

import (
	"bytes"
	"net"
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
