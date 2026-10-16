package ctr

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
)

func friendTask(task byte, args []byte) lobby.TaskRequest {
	return lobby.TaskRequest{Service: 138, Task: task, Args: demonware.NewReader(args), Payload: args}
}

func TestP2PFriendAdvertisementIsLiveReadonlyAndScoped(t *testing.T) {
	hub := newFixtureHub()
	a, pa := newRoomFixture(t, hub, 101)
	b, pb := newRoomFixture(t, hub, 102)
	attrs := &demonware.Writer{}
	attrs.Uint64(101)
	attrs.String("PlayerA")
	create := &demonware.Writer{}
	create.String("Delta")
	create.Blob(make([]byte, 67))
	create.Uint32(1)
	create.Uint32(8)
	create.Blob(attrs.Bytes())
	result := a.advertisement.Handle(pa, friendTask(1, create.Bytes()))
	hub.mu.Lock()
	early := hub.advertisements["101"] != ""
	hub.mu.Unlock()
	if early {
		t.Fatal("advertised before creation reply")
	}
	deliverResult(t, result)
	lookup := &demonware.Writer{}
	lookup.String("Delta")
	lookup.Uint64(101)
	lookup.Uint64(102)
	lookup.Uint64(999)
	reply := b.advertisement.Handle(pb, friendTask(14, lookup.Bytes())).Reply
	r := demonware.NewReader(reply)
	_, _ = r.Uint64()
	code, _ := r.Uint32()
	task, _ := r.Uint8()
	count, _ := r.Uint32()
	total, _ := r.Uint32()
	if code != 0 || task != 14 || count != 1 || total != 1 {
		t.Fatal("friend advertisement not returned")
	}
	address, _ := r.Blob()
	sid, _ := r.Blob()
	gt, _ := r.Uint32()
	maxPlayers, _ := r.Uint32()
	players, _ := r.Uint32()
	proxy, _ := rewriteCommonAddress(base64.StdEncoding.EncodeToString(make([]byte, 67)), "127.0.0.90", 43074)
	expected, _ := base64.StdEncoding.DecodeString(proxy)
	if !bytes.Equal(address, expected) || len(sid) != 8 || gt != 1 || maxPlayers != 8 || players != 1 || !bytes.Equal(r.Remaining(), attrs.Bytes()) {
		t.Fatal("friend wire record differs from 138/14 shape")
	}
	// A stale or disconnected publisher never remains joinable.
	a.advertisement.CloseSession(pa)
	reply = b.advertisement.Handle(pb, friendTask(14, lookup.Bytes())).Reply
	r = demonware.NewReader(reply)
	_, _ = r.Uint64()
	code, _ = r.Uint32()
	_, _ = r.Uint8()
	count, _ = r.Uint32()
	if code != 0 || count != 0 {
		t.Fatal("closed owner still advertised")
	}
	hub.mu.Lock()
	sent := hub.calls["peer.send"]
	hub.mu.Unlock()
	if sent != 0 {
		t.Fatal("friend reads used generic room RPC")
	}
}

func TestP2PNamesUseOnlyLocalPlatformLookup(t *testing.T) {
	hub := newFixtureHub()
	b, p := newRoomFixture(t, hub, 101)
	h := &localNameHandler{backend: b}
	w := &demonware.Writer{}
	w.Uint32(1)
	w.String("Friend")
	result := h.Handle(p, lobby.TaskRequest{Service: 12, Task: 10, Args: demonware.NewReader(w.Bytes())})
	r := demonware.NewReader(result.Reply)
	_, _ = r.Uint64()
	code, _ := r.Uint32()
	_, _ = r.Uint8()
	count, _ := r.Uint32()
	_, _ = r.Uint32()
	pid, _ := r.Uint64()
	name, _ := r.String()
	if code != 0 || count != 1 || pid != 102 || name != "Friend" {
		t.Fatal("platform name lookup failed")
	}
	hub.mu.Lock()
	sent := hub.calls["peer.send"]
	lookups := hub.calls["name.lookup"]
	hub.mu.Unlock()
	if sent != 0 || lookups != 1 {
		t.Fatal("names crossed room channel")
	}
}
