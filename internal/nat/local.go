package nat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
)

type localVirtualEndpoint struct {
	game, canonical netip.AddrPort
}

// SetLocalVirtualEndpoint freezes the negotiated mapping before listeners open.
// Only the configured game's exact physical source receives the virtual value.
func (s *Server) SetLocalVirtualEndpoint(game, canonical netip.AddrPort) error {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if !s.LocalOnly || s.conn != nil || s.stopped || !game.Addr().Is4() || !game.Addr().IsLoopback() || game.Port() == 0 || !canonical.Addr().Is4() || canonical.Port() == 0 {
		return errors.New("invalid local virtual discovery configuration")
	}
	s.localMu.Lock()
	defer s.localMu.Unlock()
	if s.localVirtual != nil {
		return errors.New("local discovery already configured")
	}
	s.localVirtual = &localVirtualEndpoint{game: game, canonical: canonical}
	return nil
}

// SetLocalPendingEndpoint reserves the provider's canonical endpoint without
// inventing a game source. Discovery stays silent until the private pipe binds
// the exact source observed by the supervising host.
func (s *Server) SetLocalPendingEndpoint(canonical netip.AddrPort) error {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if !s.LocalOnly || s.conn != nil || s.stopped || !canonical.Addr().Is4() || canonical.Port() == 0 {
		return errors.New("invalid pending local discovery configuration")
	}
	s.localMu.Lock()
	defer s.localMu.Unlock()
	if s.localVirtual != nil {
		return errors.New("local discovery already configured")
	}
	s.localVirtual = &localVirtualEndpoint{canonical: canonical}
	s.localPending, s.localExactOnly = true, true
	return nil
}

// BindLocalGameEndpoint commits once; rebinding cannot redirect an admitted
// game's canonical route. It may run while the NAT listener is active.
func (s *Server) BindLocalGameEndpoint(game netip.AddrPort) error {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.stopped || !game.Addr().Is4() || !game.Addr().IsLoopback() || game.Port() == 0 {
		return errors.New("invalid local game endpoint")
	}
	s.localMu.Lock()
	defer s.localMu.Unlock()
	if !s.localPending || s.localVirtual == nil {
		return errors.New("local game endpoint is not pending")
	}
	s.localVirtual.game = game
	s.localPending = false
	return nil
}

func (s *Server) localDiscoveryReply(packet []byte, source *net.UDPAddr, serverIP net.IP, port int) []byte {
	actual := source.AddrPort()
	actual = netip.AddrPortFrom(actual.Addr().Unmap(), actual.Port())
	s.localMu.RLock()
	var endpoint localVirtualEndpoint
	if s.localVirtual != nil {
		endpoint = *s.localVirtual
	}
	pending, exactOnly := s.localPending, s.localExactOnly
	s.localMu.RUnlock()
	if pending || (exactOnly && actual != endpoint.game) {
		return nil
	}
	reply := localDiscoveryReply(packet, source, serverIP, port)
	if len(reply) == 0 || !endpoint.canonical.IsValid() || actual != endpoint.game {
		return reply
	}
	ip := endpoint.canonical.Addr().As4()
	copy(reply[3:7], ip[:])
	binary.LittleEndian.PutUint16(reply[7:9], endpoint.canonical.Port())
	return reply
}

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
