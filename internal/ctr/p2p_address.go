package ctr

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strconv"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// CommonAddr's eleven IPv4/u16le endpoints and final NAT byte are established
// by the owned PS4 serializer. Whether race datagrams embed addresses still
// requires game acceptance; this adapter rewrites only room documents.
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
	address, err := rewriteCommonAddress(listen.HostAddress, endpoint.Address, endpoint.Port)
	if err != nil {
		return "", err
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
