package ctr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
)

func virtualCommonAddr(endpoint netip.AddrPort) []byte {
	raw := make([]byte, 67)
	ip := endpoint.Addr().As4()
	for slot := 0; slot < 11; slot++ {
		copy(raw[slot*6:], ip[:])
		binary.LittleEndian.PutUint16(raw[slot*6+4:], endpoint.Port())
	}
	raw[66] = 3
	return raw
}

type virtualFixtureTransport struct {
	RoomTransport
	endpoint netip.AddrPort
	calls    int
	fail     bool
}

func (t *virtualFixtureTransport) Call(_ context.Context, op string, fields map[string]any, out any) error {
	if op != "peer.endpoint" || fields["peer"] != "102" || fields["room_id"] != "test-room" {
		return errors.New("unexpected fixture operation")
	}
	t.calls++
	if t.fail {
		return errors.New("fixture endpoint unavailable")
	}
	raw, _ := json.Marshal(map[string]any{"address": t.endpoint.Addr().String(), "port": t.endpoint.Port()})
	return json.Unmarshal(raw, out)
}

func TestVirtualCommonAddrPreservesIdentityAndRejectsOtherEndpoints(t *testing.T) {
	endpoint := netip.MustParseAddrPort("198.18.5.6:12345")
	raw := virtualCommonAddr(endpoint)
	// The serializer's invalid local slot must remain exactly intact too.
	copy(raw[6:12], []byte{0, 255, 0, 255, 0, 0})
	// NAT can change the public port; the local candidate is not the identity.
	binary.LittleEndian.PutUint16(raw[4:6], 3074)
	encoded := base64.StdEncoding.EncodeToString(raw)
	b := &P2PBackend{canonical: netip.MustParseAddrPort("198.19.4.3:23456")}
	got, err := b.peerCommonAddress(encoded, endpoint.Addr().String(), endpoint.Port())
	if err != nil || got != encoded {
		t.Fatal("canonical address bytes changed")
	}
	for _, change := range []func([]byte){
		func(p []byte) { p[60] ^= 1 }, // identity-bearing public IP
		func(p []byte) { p[64] ^= 1 }, // identity-bearing public port
		func(p []byte) { copy(p[60:66], []byte{0, 255, 0, 255, 0, 0}) },
	} {
		changed := bytes.Clone(raw)
		change(changed)
		if _, err := b.peerCommonAddress(base64.StdEncoding.EncodeToString(changed), endpoint.Addr().String(), endpoint.Port()); err == nil {
			t.Fatal("foreign or malformed endpoint accepted")
		}
	}
	if _, err := b.peerCommonAddress(encoded, "127.0.0.2", 12345); err == nil {
		t.Fatal("physical proxy accepted in virtual mode")
	}
}

func TestVirtualHostDocumentRemainsExactAndRequiresAuthorizedEndpoint(t *testing.T) {
	endpoint := netip.MustParseAddrPort("198.18.5.6:12345")
	transport := &virtualFixtureTransport{endpoint: endpoint}
	b := &P2PBackend{ctx: context.Background(), owner: 101, canonical: netip.MustParseAddrPort("198.19.4.3:23456"), transport: transport, room: directoryRoom{ID: "test-room", Members: []string{"101", "102"}}}
	raw := `{"listen_server": {"host_player_id":102,"host_address":"` + base64.StdEncoding.EncodeToString(virtualCommonAddr(endpoint)) + `","security_key":"synthetic-key","security_id":"synthetic-id"}, "unchanged":true}`
	got, err := b.rewriteHost(raw)
	if err != nil || got != raw || transport.calls != 1 {
		t.Fatal("host document changed or authorization missing")
	}
	transport.fail = true
	if _, err := b.rewriteHost(raw); err == nil {
		t.Fatal("failed endpoint allocation produced usable document")
	}
	transport.fail = false
	transport.endpoint = netip.MustParseAddrPort("198.19.7.8:12345")
	if _, err := b.rewriteHost(raw); err == nil {
		t.Fatal("document for another canonical identity accepted")
	}
	b.room.Members = []string{"101"}
	calls := transport.calls
	if _, err := b.rewriteHost(raw); err == nil || transport.calls != calls {
		t.Fatal("nonmember endpoint requested")
	}
}

type virtualAdvertisementTransport struct {
	RoomTransport
	address       netip.AddrPort
	payload       string
	endpointCalls int
	fail          bool
}

func (t *virtualAdvertisementTransport) Call(_ context.Context, op string, fields map[string]any, out any) error {
	if fields["peer"] != "102" {
		return errors.New("unexpected peer")
	}
	var result any
	switch op {
	case "advertisement.get":
		result = map[string]any{"payload": t.payload}
	case "peer.endpoint":
		if fields["scope"] != "friend" {
			return errors.New("unexpected scope")
		}
		t.endpointCalls++
		if t.fail {
			return errors.New("endpoint unavailable")
		}
		result = map[string]any{"address": t.address.Addr().String(), "port": t.address.Port()}
	default:
		return errors.New("unexpected operation")
	}
	raw, _ := json.Marshal(result)
	return json.Unmarshal(raw, out)
}

func TestVirtualFriendRecordPreservesAddressAndSecurityFields(t *testing.T) {
	canonical := netip.MustParseAddrPort("198.18.5.6:12345")
	address := virtualCommonAddr(canonical)
	binary.LittleEndian.PutUint16(address[4:6], 3074)
	ad := friendAdvertisement{Version: 1, Owner: "102", Context: "Delta", Session: lobby.Advertisement{Address: address, SessionID: []byte("test-sid"), GameType: 1, MaxPlayers: 8, NumPlayers: 1, Attributes: []byte("opaque-attributes")}}
	raw, _ := json.Marshal(ad)
	transport := &virtualAdvertisementTransport{address: canonical, payload: base64.StdEncoding.EncodeToString(raw)}
	b := &P2PBackend{ctx: context.Background(), canonical: netip.MustParseAddrPort("198.19.4.3:23456"), owner: 101, transport: transport, engine: &asyncHandler{ReplyCounter: &lobby.ReplyCounter{}}}
	h := &friendAdvertisementHandler{backend: b}
	peer := &testPeer{N: 101}
	lookup := func() *demonware.Reader {
		w := &demonware.Writer{}
		w.Uint64(102)
		result := h.lookup(peer, demonware.NewReader(w.Bytes()))
		return demonware.NewReader(result.Reply)
	}
	r := lookup()
	_, _ = r.Uint64()
	_, _ = r.Uint32()
	_, _ = r.Uint8()
	count, _ := r.Uint32()
	total, _ := r.Uint32()
	gotAddress, _ := r.Blob()
	gotSID, _ := r.Blob()
	gameType, _ := r.Uint32()
	maxPlayers, _ := r.Uint32()
	players, _ := r.Uint32()
	if count != 1 || total != 1 || !bytes.Equal(gotAddress, address) || !bytes.Equal(gotSID, ad.Session.SessionID) || gameType != 1 || maxPlayers != 8 || players != 1 || !bytes.Equal(r.Remaining(), ad.Session.Attributes) || transport.endpointCalls != 1 {
		t.Fatal("virtual friend record changed or endpoint not authorized")
	}
	for _, fail := range []bool{true, false} {
		transport.fail = fail
		transport.address = netip.MustParseAddrPort("198.18.5.7:12345")
		r = lookup()
		_, _ = r.Uint64()
		_, _ = r.Uint32()
		_, _ = r.Uint8()
		count, _ = r.Uint32()
		if count != 0 {
			t.Fatal("unavailable or mismatched peer endpoint accepted")
		}
	}
}

func TestVirtualOwnerInvalidAdvertisementWithdrawsPreviousRecord(t *testing.T) {
	hub := newFixtureHub()
	b, peer := newRoomFixture(t, hub, 101)
	b.canonical = netip.MustParseAddrPort("198.18.5.6:12345")
	address := virtualCommonAddr(b.canonical)
	publish := func(raw []byte) {
		w := &demonware.Writer{}
		w.String("Delta")
		w.Blob(raw)
		w.Uint32(1)
		w.Uint32(8)
		w.Blob(nil)
		deliverResult(t, b.advertisement.Handle(peer, friendTask(1, w.Bytes())))
	}
	publish(address)
	hub.mu.Lock()
	valid := hub.advertisements["101"] != ""
	hub.mu.Unlock()
	if !valid {
		t.Fatal("valid owner advertisement missing")
	}
	address[60] ^= 1
	publish(address)
	hub.mu.Lock()
	stale := hub.advertisements["101"] != ""
	hub.mu.Unlock()
	if stale {
		t.Fatal("invalid owner update retained stale advertisement")
	}
}
