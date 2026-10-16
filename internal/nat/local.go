package nat

import (
	"bytes"
	"encoding/binary"
	"net"
)

// These exact request/reply shapes were observed in the owned CTR PS4 build;
// flags 2/3 and traversal packets remain unsupported, not guessed NAT states.
func localDiscoveryReply(packet []byte, source *net.UDPAddr, serverIP net.IP, port int) []byte {
	if !source.IP.IsLoopback() || source.IP.To4() == nil || !serverIP.IsLoopback() || serverIP.To4() == nil {
		return nil
	}
	var reply []byte
	switch {
	case bytes.Equal(packet, []byte{30, 3, 0}):
		reply = []byte{31, 2, 0}
	case bytes.Equal(packet, []byte{20, 2, 0, 0}):
		reply = []byte{21, 2, 0}
	default:
		return nil
	}
	reply = append(reply, source.IP.To4()...)
	reply = binary.LittleEndian.AppendUint16(reply, uint16(source.Port))
	if packet[0] == 20 {
		reply = append(reply, serverIP.To4()...)
		reply = binary.LittleEndian.AppendUint16(reply, uint16(port))
	}
	return reply
}
