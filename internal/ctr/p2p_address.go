package ctr

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// Legacy ABI-2 hosts request physical proxies and retain their earlier document
// adapter. Virtual endpoint hosts must preserve the identity-bearing bytes.
func rewriteCommonAddress(encoded, address string, port uint16) (string, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	ip := net.ParseIP(address)
	if err != nil || len(raw) != 67 || ip == nil || ip.To4() == nil || !ip.IsLoopback() || port == 0 {
		return "", errors.New("invalid peer proxy endpoint")
	}
	for slot := 0; slot < 11; slot++ {
		copy(raw[slot*6:slot*6+4], ip.To4())
		binary.LittleEndian.PutUint16(raw[slot*6+4:slot*6+6], port)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func validCanonicalEndpoint(endpoint netip.AddrPort) bool {
	return endpoint.Addr().Is4() && endpoint.Addr() != netip.MustParseAddr("0.255.0.255") && endpoint.Port() != 0
}

// The original game hashes the public endpoint into CommonAddr ID. Local
// candidates can legitimately have another port (for example the game's 3074),
// so preserve them. The host route refuses unmapped candidates before OS I/O.
func validateCanonicalCommonAddress(raw []byte, endpoint netip.AddrPort) bool {
	if len(raw) != 67 || !validCanonicalEndpoint(endpoint) {
		return false
	}
	ip := netip.AddrFrom4([4]byte(raw[60:64]))
	port := binary.LittleEndian.Uint16(raw[64:66])
	return ip == endpoint.Addr() && port == endpoint.Port()
}

func (b *P2PBackend) peerCommonAddress(encoded, address string, port uint16) (string, error) {
	if !b.canonical.IsValid() {
		return rewriteCommonAddress(encoded, address, port)
	}
	ip, err := netip.ParseAddr(address)
	raw, decodeErr := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || decodeErr != nil || !validateCanonicalCommonAddress(raw, netip.AddrPortFrom(ip, port)) {
		return "", errors.New("peer CommonAddr does not match canonical endpoint")
	}
	return encoded, nil
}

func (b *P2PBackend) rewriteHost(raw string) (string, error) {
	var host map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &host) != nil {
		return "", errors.New("invalid room host document")
	}
	var listen struct {
		HostAddress  string `json:"host_address"`
		HostPlayerID uint64 `json:"host_player_id"`
	}
	if json.Unmarshal(host["listen_server"], &listen) != nil || listen.HostPlayerID == 0 {
		return "", errors.New("invalid room host identity")
	}
	if listen.HostPlayerID == b.owner {
		if b.canonical.IsValid() {
			raw, err := base64.StdEncoding.Strict().DecodeString(listen.HostAddress)
			if err != nil || !validateCanonicalCommonAddress(raw, b.canonical) {
				return "", errors.New("owner CommonAddr does not match canonical endpoint")
			}
		}
		return raw, nil
	}
	b.mu.Lock()
	room := b.room
	b.mu.Unlock()
	pid := strconv.FormatUint(listen.HostPlayerID, 10)
	if !containsMember(room.Members, pid) {
		return "", errors.New("room host is not a member")
	}
	var endpoint struct {
		Address string `json:"address"`
		Port    uint16 `json:"port"`
	}
	if err := b.transport.Call(b.ctx, "peer.endpoint", map[string]any{"room_id": room.ID, "peer": pid}, &endpoint); err != nil {
		return "", err
	}
	address, err := b.peerCommonAddress(listen.HostAddress, endpoint.Address, endpoint.Port)
	if err != nil {
		return "", err
	}
	if b.canonical.IsValid() {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(host["listen_server"], &fields)
	fields["host_address"], _ = json.Marshal(address)
	host["listen_server"], _ = json.Marshal(fields)
	out, err := json.Marshal(host)
	return string(out), err
}

func (b *P2PBackend) rewritePush(event uint32, raw []byte) ([]byte, error) {
	if !roomEvent(event) {
		return nil, errors.New("unsupported room push")
	}
	count := 2
	if event == EventCreateLobby {
		count = 3
	}
	if event == EventMergeLobby {
		count = 3
	}
	r := demonware.NewReader(raw)
	w := &demonware.Writer{}
	for i := 0; i < count; i++ {
		id, err := r.Uint64()
		if err != nil {
			return nil, err
		}
		w.Uint64(id)
	}
	host, e1 := r.String()
	backend, e2 := r.String()
	if e1 != nil || e2 != nil || len(r.Remaining()) != 0 {
		return nil, errors.New("invalid room push documents")
	}
	host, err := b.rewriteHost(host)
	if err != nil {
		return nil, err
	}
	w.String(host)
	w.String(backend)
	return w.Bytes(), nil
}

func (b *P2PBackend) rewriteReply(raw []byte) ([]byte, error) {
	r := demonware.NewReader(raw)
	transaction, e1 := r.Uint64()
	code, e2 := r.Uint32()
	if e1 != nil || e2 != nil {
		return nil, errors.New("invalid room reply")
	}
	if code != 0 {
		return raw, nil
	}
	task, e1 := r.Uint8()
	count, e2 := r.Uint32()
	total, e3 := r.Uint32()
	if e1 != nil || e2 != nil || e3 != nil || task != TaskGetLobbyDocuments || count != 1 || total != 1 {
		return nil, errors.New("invalid document reply")
	}
	host, e1 := r.String()
	backend, e2 := r.String()
	if e1 != nil || e2 != nil || len(r.Remaining()) != 0 {
		return nil, errors.New("invalid document reply body")
	}
	host, err := b.rewriteHost(host)
	if err != nil {
		return nil, err
	}
	return demonware.TaskReply(transaction, task, 0, func(w *demonware.Writer) uint32 { w.String(host); w.String(backend); return 1 }), nil
}
