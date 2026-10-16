package ctr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/companion"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
)

type roomFixtureHub struct {
	mu             sync.Mutex
	rooms          map[string]directoryRoom
	clients        map[string]*roomFixtureTransport
	next           int
	calls          map[string]int
	advertisements map[string]string
	failMembership bool
}
type roomFixtureTransport struct {
	hub    *roomFixtureHub
	pid    string
	events chan companion.Event
	done   chan struct{}
}

func (t *roomFixtureTransport) Events() <-chan companion.Event { return t.events }
func (t *roomFixtureTransport) Done() <-chan struct{}          { return t.done }
func (t *roomFixtureTransport) Call(ctx context.Context, op string, args map[string]any, out any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	h := t.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls[op]++
	var result any = map[string]any{}
	id, _ := args["room_id"].(string)
	room := h.rooms[id]
	switch op {
	case "directory.search":
		rooms := []directoryRoom{}
		metadata := args["metadata"].(map[string]any)
		for _, r := range h.rooms {
			if r.Metadata["playlist"] == metadata["playlist"] {
				r.Members = nil // Steam does not disclose membership before joining.
				rooms = append(rooms, r)
			}
		}
		result = map[string]any{"rooms": rooms}
	case "directory.create":
		h.next++
		room = directoryRoom{ID: strconv.Itoa(h.next), Owner: t.pid, Members: []string{t.pid}, Metadata: args["metadata"].(map[string]any)}
		h.rooms[room.ID] = room
		result = room
	case "directory.join":
		if room.ID == "" {
			return errors.New("unknown room")
		}
		room.Members = append(append([]string(nil), room.Members...), t.pid)
		h.rooms[id] = room
		result = room
		for _, pid := range room.Members {
			h.clients[pid].events <- companion.Event{Event: "room.changed", RoomID: id, Owner: room.Owner, Members: append([]string(nil), room.Members...)}
		}
	case "directory.update":
		if room.Owner != t.pid {
			return errors.New("not owner")
		}
		room.Metadata = args["metadata"].(map[string]any)
		h.rooms[id] = room
		result = room
	case "directory.members":
		if h.failMembership || room.ID == "" || !containsMember(room.Members, t.pid) {
			return errors.New("not member")
		}
		result = room
	case "directory.leave":
		if room.ID == "" {
			return nil
		}
		if room.Owner == t.pid {
			delete(h.rooms, id)
			for _, pid := range room.Members {
				if pid != t.pid {
					h.clients[pid].events <- companion.Event{Event: "room.closed", RoomID: id}
				}
			}
		} else {
			var members []string
			for _, pid := range room.Members {
				if pid != t.pid {
					members = append(members, pid)
				}
			}
			room.Members = members
			h.rooms[id] = room
			for _, pid := range members {
				h.clients[pid].events <- companion.Event{Event: "room.changed", RoomID: id, Owner: room.Owner, Members: members}
			}
		}
	case "peer.send":
		peer := args["peer"].(string)
		if !containsMember(room.Members, peer) || !containsMember(room.Members, t.pid) {
			return errors.New("not member")
		}
		h.clients[peer].events <- companion.Event{Event: "peer.message", RoomID: id, Sender: t.pid, Payload: args["payload"].(string)}
	case "peer.endpoint":
		result = map[string]any{"address": "127.0.0.90", "port": 43074}
	case "advertisement.set":
		if args["context"] != advertisementContext {
			return errors.New("unexpected advertisement")
		}
		h.advertisements[t.pid] = args["payload"].(string)
	case "advertisement.get":
		peer := args["peer"].(string)
		if args["context"] != advertisementContext || h.advertisements[peer] == "" {
			return errors.New("not found")
		}
		result = map[string]any{"payload": h.advertisements[peer]}
	case "name.lookup":
		result = map[string]any{"players": []map[string]string{{"steam_id": "102", "online_id": "Friend"}}}
	case "name.resolve":
		result = map[string]any{"online_id": "Friend"}
	default:
		return errors.New("unsupported fixture operation")
	}
	if out != nil {
		raw, _ := json.Marshal(result)
		return json.Unmarshal(raw, out)
	}
	return nil
}

type capturedPush struct {
	event uint32
	data  []byte
}
type roomFixturePeer struct {
	*testPeer
	pushes chan capturedPush
}

func (p *roomFixturePeer) SendPush(event uint32, write func(*demonware.Writer)) error {
	w := &demonware.Writer{}
	write(w)
	p.pushes <- capturedPush{event, append([]byte(nil), w.Bytes()...)}
	return nil
}

func newRoomFixture(t *testing.T, hub *roomFixtureHub, pid uint64) (*P2PBackend, *roomFixturePeer) {
	t.Helper()
	id := strconv.FormatUint(pid, 10)
	transport := &roomFixtureTransport{hub: hub, pid: id, events: make(chan companion.Event, 256), done: make(chan struct{})}
	hub.clients[id] = transport
	peer := &roomFixturePeer{testPeer: &testPeer{N: pid, Player: &platform.PlayerID{PID: pid, Title: 5760}}, pushes: make(chan capturedPush, 32)}
	m := NewMatchmaker(func() []uint64 { return nil })
	m.Clock = func() time.Time { return time.Unix(1000, 0) }
	h := &asyncHandler{ReplyCounter: &lobby.ReplyCounter{}, matchmaker: m, info: map[uint64]string{}}
	state := presence.NewStore(nil)
	contextHandler := &contextHandler{&lobby.ContextHandler{ReplyCounter: h.ReplyCounter, ReadInfo: ReadInfo, Registry: lobby.NewSessionRegistry(), Presence: state, Relay: relay.NewTable(&diagnostics.Diagnostics{RedactLogs: true}, "127.0.0.1", "")}}
	table := lobby.NewServiceTable(h.ReplyCounter, map[byte]lobby.TaskHandler{145: h, 138: contextHandler, 12: &utilityHandler{&presence.Handler{Replies: h.ReplyCounter, Presence: state}}}, nil)
	b, err := InstallP2P(context.Background(), table, transport, pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(); b.Wait() })
	return b, peer
}

func newFixtureHub() *roomFixtureHub {
	return &roomFixtureHub{rooms: map[string]directoryRoom{}, clients: map[string]*roomFixtureTransport{}, calls: map[string]int{}, advertisements: map[string]string{}}
}

func roomInfo() string {
	info := PlayerInfo{}
	info.Listen.Address = base64.StdEncoding.EncodeToString(make([]byte, 67))
	info.Listen.ID = base64.StdEncoding.EncodeToString(make([]byte, 8))
	info.Listen.Key = base64.StdEncoding.EncodeToString(make([]byte, 16))
	raw, _ := json.Marshal(info)
	return string(raw)
}
func stringArgs(raw string) []byte { w := &demonware.Writer{}; w.String(raw); return w.Bytes() }
func searchArgs(pid uint64) []byte {
	return stringArgs(fmt.Sprintf(`{"members":[%d],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":0,"matchmaking_version":200}}}`, pid))
}
func deliverResult(t *testing.T, result lobby.TaskResult) {
	t.Helper()
	if !successfulReply(result.Reply) {
		t.Fatal("task failed")
	}
	if result.AfterReply != nil {
		result.AfterReply(nil)
	}
}
func awaitRoomPush(t *testing.T, p *roomFixturePeer) capturedPush {
	t.Helper()
	select {
	case push := <-p.pushes:
		return push
	case <-time.After(3 * time.Second):
		t.Fatal("missing room push")
		return capturedPush{}
	}
}
func pushDocuments(t *testing.T, push capturedPush) ([]uint64, string, string) {
	t.Helper()
	r := demonware.NewReader(push.data)
	count := 2
	if push.event == EventCreateLobby || push.event == EventMergeLobby {
		count = 3
	}
	ids := make([]uint64, count)
	for i := range ids {
		var err error
		ids[i], err = r.Uint64()
		if err != nil {
			t.Fatal(err)
		}
	}
	h, e1 := r.String()
	b, e2 := r.String()
	if e1 != nil || e2 != nil {
		t.Fatal("invalid push docs")
	}
	return ids, h, b
}

func TestP2PEngineMatchesCentralNotices(t *testing.T) {
	hub := newFixtureHub()
	a, pa := newRoomFixture(t, hub, 101)
	b, pb := newRoomFixture(t, hub, 102)
	deliverResult(t, a.Handle(pa, taskRequest(2, stringArgs(roomInfo()))))
	deliverResult(t, a.Handle(pa, taskRequest(18, searchArgs(pa.PID()))))
	deliverResult(t, b.Handle(pb, taskRequest(2, stringArgs(roomInfo()))))
	result := b.Handle(pb, taskRequest(18, searchArgs(pb.PID())))
	if !successfulReply(result.Reply) {
		t.Fatal("remote search failed")
	}
	select {
	case <-pa.pushes:
		t.Fatal("engine activated before member reply delivery")
	default:
	}
	deliverResult(t, result)
	created := awaitRoomPush(t, pa)
	if created.event != EventCreateLobby {
		t.Fatal("missing owner create")
	}
	ids, host, _ := pushDocuments(t, created)
	w := &demonware.Writer{}
	w.Uint64(ids[1])
	w.Uint64(ids[2])
	w.String(host)
	deliverResult(t, a.Handle(pa, taskRequest(15, w.Bytes())))
	joined := awaitRoomPush(t, pb)
	if joined.event != EventJoinLobby {
		t.Fatal("missing member join")
	}
	get := &demonware.Writer{}
	get.Uint64(ids[1])
	documents := b.Handle(pb, taskRequest(TaskGetLobbyDocuments, get.Bytes()))
	deliverResult(t, documents)
	r := demonware.NewReader(documents.Reply)
	_, _ = r.Uint64()
	_, _ = r.Uint32()
	_, _ = r.Uint8()
	_, _ = r.Uint32()
	_, _ = r.Uint32()
	documentHost, err := r.String()
	if err != nil {
		t.Fatal(err)
	}
	_, joinHost, _ := pushDocuments(t, joined)
	if documentHost != joinHost {
		t.Fatal("document query did not apply the same proxy mapping")
	}
	// A stale disband or cancellation must not leave the current directory room.
	stale := &demonware.Writer{}
	stale.Uint64(9999)
	deliverResult(t, b.Handle(pb, taskRequest(TaskLobbyDisbanded, stale.Bytes())))
	deliverResult(t, b.Handle(pb, taskRequest(8, stale.Bytes())))
	b.mu.Lock()
	stillJoined := b.room.ID != ""
	b.mu.Unlock()
	if !stillJoined {
		t.Fatal("stale leave removed current room")
	}
	// Execute the same engine inputs centrally and compare notice bodies. Only
	// the member-local proxy address may differ from the central document.
	m := matchFixture{Matchmaker: *NewMatchmaker(func() []uint64 { return nil })}
	m.Clock = func() time.Time { return time.Unix(1000, 0) }
	sa, sb := testCTRSearchFor(&m, t, 101, 0), testCTRSearchFor(&m, t, 102, 0)
	m.Enqueue(sa)
	centralCreate := m.Enqueue(sb)[0]
	_, notices := m.SyncDoc(sa.Conn, centralCreate.IDs[1], 1, centralCreate.Host)
	for i, push := range []capturedPush{created, joined} {
		central := centralCreate
		if i == 1 {
			central = notices[0]
		}
		gotIDs, gotHost, gotBackend := pushDocuments(t, push)
		if push.event != central.Event || !reflect.DeepEqual(gotIDs, central.IDs) {
			t.Fatal("P2P event IDs differ from central")
		}
		var expected, actual map[string]any
		_ = json.Unmarshal([]byte(central.Host), &expected)
		_ = json.Unmarshal([]byte(gotHost), &actual)
		if i == 1 {
			proxy, err := rewriteCommonAddress(base64.StdEncoding.EncodeToString(make([]byte, 67)), "127.0.0.90", 43074)
			if err != nil {
				t.Fatal(err)
			}
			if actual["listen_server"].(map[string]any)["host_address"] != proxy {
				t.Fatal("member received another machine's loopback address")
			}
			actual["listen_server"] = expected["listen_server"]
		}
		if !reflect.DeepEqual(actual, expected) || gotBackend != central.Backend {
			t.Fatal("P2P notice semantics differ from central")
		}
	}
	// Owner loss invalidates the room; no automatic engine migration occurs.
	a.leaveRoom()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		gone := b.room.ID == ""
		b.mu.Unlock()
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner loss kept stale room")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestP2PRejectsForeignIdentityAndNonRoomTasks(t *testing.T) {
	hub := newFixtureHub()
	b, p := newRoomFixture(t, hub, 101)
	deliverResult(t, b.Handle(p, taskRequest(2, stringArgs(roomInfo()))))
	deliverResult(t, b.Handle(p, taskRequest(18, searchArgs(101))))
	b.mu.Lock()
	room := b.room
	b.room.Members = append(b.room.Members, "102")
	b.mu.Unlock()
	for _, packet := range []roomPacket{
		{Version: 1, Kind: "request", ID: "1", PID: "103", Conn: "1", Task: 2, Data: stringArgs(roomInfo())},
		{Version: 1, Kind: "request", ID: "2", PID: "102", Conn: "1", Task: 245},
		{Version: 1, Kind: "request", ID: "3", PID: "102", Conn: "1", Task: 18, Data: searchArgs(999)},
		{Version: 2, Kind: "request", ID: "4", PID: "102", Conn: "1", Task: 2, Data: stringArgs(roomInfo())},
	} {
		raw, _ := json.Marshal(packet)
		b.receive(companion.Event{Event: "peer.message", RoomID: room.ID, Sender: "102", Payload: base64.StdEncoding.EncodeToString(raw)})
	}
	b.mu.Lock()
	if len(b.remotes) != 0 || len(b.acks) != 0 {
		t.Fatal("foreign or non-room input reached engine")
	}
	b.mu.Unlock()
	if successfulReply(b.Handle(p, taskRequest(99, nil)).Reply) {
		t.Fatal("unknown async task fabricated success")
	}
	for _, address := range []string{"192.168.1.1", "0.0.0.0", "example.test"} {
		if _, err := rewriteCommonAddress(base64.StdEncoding.EncodeToString(make([]byte, 67)), address, 3074); err == nil {
			t.Fatal("nonloopback proxy accepted")
		}
	}
}

func TestP2PFailedMembershipSnapshotLeavesDirectory(t *testing.T) {
	hub := newFixtureHub()
	b, p := newRoomFixture(t, hub, 101)
	deliverResult(t, b.Handle(p, taskRequest(2, stringArgs(roomInfo()))))
	hub.failMembership = true
	if successfulReply(b.Handle(p, taskRequest(18, searchArgs(101))).Reply) {
		t.Fatal("failed membership snapshot accepted")
	}
	b.mu.Lock()
	selected := b.room.ID != ""
	b.mu.Unlock()
	hub.mu.Lock()
	rooms := len(hub.rooms)
	hub.mu.Unlock()
	if selected || rooms != 0 {
		t.Fatal("failed selection left stale directory membership")
	}
}

func TestP2PLeaveCancelsPendingAndRejectsOlderConnection(t *testing.T) {
	hub := newFixtureHub()
	a, pa := newRoomFixture(t, hub, 101)
	b, pb := newRoomFixture(t, hub, 102)
	deliverResult(t, a.Handle(pa, taskRequest(2, stringArgs(roomInfo()))))
	deliverResult(t, a.Handle(pa, taskRequest(18, searchArgs(101))))
	deliverResult(t, b.Handle(pb, taskRequest(2, stringArgs(roomInfo()))))
	raw, _ := demonware.NewReader(searchArgs(102)).String()
	search, err := NewMatchmaker(nil).ParseSearch(pb, raw, roomInfo())
	if err != nil || b.selectRoom(search) != nil {
		t.Fatal("fixture join failed")
	}
	b.mu.Lock()
	room := b.room
	b.mu.Unlock()
	// Prevent the owner from processing the new request while the member leaves.
	a.mutationMu.Lock()
	done := make(chan error, 1)
	go func() { _, err := b.remoteCall(pb, room, 2, stringArgs(roomInfo())); done <- err }()
	end := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		pending := len(b.pending)
		b.mu.Unlock()
		if pending != 0 {
			break
		}
		if time.Now().After(end) {
			a.mutationMu.Unlock()
			t.Fatal("request was not queued")
		}
		time.Sleep(time.Millisecond)
	}
	b.leaveRoom()
	select {
	case err := <-done:
		if err == nil {
			t.Error("closed request succeeded")
		}
	case <-time.After(time.Second):
		t.Error("closed request waited for network timeout")
	}
	a.mutationMu.Unlock()
	// The same remote PID may replace its connection only with a newer generation.
	a.mutationMu.Lock()
	current := a.remotePeer("102", "9")
	stale := a.remotePeer("102", "8")
	a.mutationMu.Unlock()
	if current == nil || stale != nil {
		t.Fatal("older remote connection replaced current one")
	}
}

func TestP2PPartyFollowerDepartureMatchesCentral(t *testing.T) {
	hub := newFixtureHub()
	a, pa := newRoomFixture(t, hub, 101)
	b, pb := newRoomFixture(t, hub, 102)
	for _, fixture := range []struct {
		backend *P2PBackend
		peer    *roomFixturePeer
	}{{a, pa}, {b, pb}} {
		deliverResult(t, fixture.backend.Handle(fixture.peer, taskRequest(2, stringArgs(roomInfo()))))
		raw, _ := demonware.NewReader(searchArgs(fixture.peer.PID())).String()
		search, err := NewMatchmaker(nil).ParseSearch(fixture.peer, raw, roomInfo())
		if err != nil || fixture.backend.selectRoom(search) != nil {
			t.Fatal("fixture directory selection")
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		a.mu.Lock()
		count := len(a.room.Members)
		a.mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("follower not observed")
		}
		time.Sleep(time.Millisecond)
	}
	partyArgs := stringArgs(`{"members":[101,102],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":0,"matchmaking_version":200}}}`)
	deliverResult(t, a.Handle(pa, taskRequest(18, partyArgs)))
	created := awaitRoomPush(t, pa)
	if created.event != EventCreateLobby {
		t.Fatal("party did not create room")
	}
	// A follower never sent a room RPC or owned a search. Its departure must
	// nevertheless remove the seat claimed by the leader, as the central engine does.
	b.leaveRoom()
	updated := awaitRoomPush(t, pa)
	if updated.event != EventUpdateLobby {
		t.Fatal("follower departure did not notify leader")
	}
	_, _, gotBackend := pushDocuments(t, updated)
	central := matchFixture{Matchmaker: *NewMatchmaker(func() []uint64 { return nil })}
	central.Clock = func() time.Time { return time.Unix(1000, 0) }
	leader := partySearchFor(&central, t, 101, 102)
	central.Enqueue(leader)
	follower := testCTRSearchFor(&central, t, 102, 0).Conn
	notices := central.Leave(follower, 0)
	if len(notices) != 1 || gotBackend != notices[0].Backend {
		t.Fatal("P2P follower leave differs from central membership")
	}
}

func searchResultID(t *testing.T, result lobby.TaskResult) uint64 {
	t.Helper()
	r := demonware.NewReader(result.Reply)
	_, _ = r.Uint64()
	_, _ = r.Uint32()
	_, _ = r.Uint8()
	_, _ = r.Uint32()
	_, _ = r.Uint32()
	raw, err := r.String()
	var resultDoc struct {
		ID uint64 `json:"mm_id"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &resultDoc) != nil || resultDoc.ID == 0 {
		t.Fatal("invalid search result")
	}
	return resultDoc.ID
}

func TestP2PValidCancelAndOwnerDeparturePolicy(t *testing.T) {
	for _, ownerCancels := range []bool{false, true} {
		name := "member"
		if ownerCancels {
			name = "owner"
		}
		t.Run(name, func(t *testing.T) {
			hub := newFixtureHub()
			a, pa := newRoomFixture(t, hub, 101)
			b, pb := newRoomFixture(t, hub, 102)
			searchIDs := []uint64{}
			for _, fixture := range []struct {
				backend *P2PBackend
				peer    *roomFixturePeer
			}{{a, pa}, {b, pb}} {
				deliverResult(t, fixture.backend.Handle(fixture.peer, taskRequest(2, stringArgs(roomInfo()))))
				args := stringArgs(fmt.Sprintf(`{"members":[%d],"lobby_slots":{"min":8,"max":8},"ruleset_payload":{"filter":{"playlist_id":0,"matchmaking_version":200}}}`, fixture.peer.PID()))
				result := fixture.backend.Handle(fixture.peer, taskRequest(18, args))
				searchIDs = append(searchIDs, searchResultID(t, result))
				deliverResult(t, result)
			}
			central := matchFixture{Matchmaker: *NewMatchmaker(func() []uint64 { return nil })}
			ca, cb := testCTRSearchFor(&central, t, 101, 0), testCTRSearchFor(&central, t, 102, 0)
			ca.Doc.Slots.Min, cb.Doc.Slots.Min = 8, 8
			central.Enqueue(ca)
			central.Enqueue(cb)
			backend, peer, id, centralSearch := b, pb, searchIDs[1], cb
			if ownerCancels {
				backend, peer, id, centralSearch = a, pa, searchIDs[0], ca
			}
			cancel := &demonware.Writer{}
			cancel.Uint64(id)
			result := backend.Handle(peer, taskRequest(8, cancel.Bytes()))
			if !replyBool(result.Reply) || !central.Cancel(centralSearch.Conn, centralSearch.ID) {
				t.Fatal("valid cancel differs from central result")
			}
			deliverResult(t, result)
			deadline := time.Now().Add(time.Second)
			for {
				b.mu.Lock()
				memberLeft := b.room.ID == ""
				b.mu.Unlock()
				a.engine.matchmaker.mu.Lock()
				waiting := len(a.engine.matchmaker.waiting)
				a.engine.matchmaker.mu.Unlock()
				expected := 1
				if ownerCancels {
					expected = 0
				}
				if memberLeft && waiting == expected {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("cancel did not settle room membership")
				}
				time.Sleep(time.Millisecond)
			}
			if len(central.waiting) != 1 {
				t.Fatal("central reference unexpectedly discarded another search")
			}
			a.mu.Lock()
			ownerRetained := a.room.ID != ""
			a.mu.Unlock()
			if ownerRetained == ownerCancels {
				t.Fatal("owner departure policy differs")
			}
			// The deliberate P2P difference: cancelling the directory owner closes
			// the entire authority. A central service survives another player leaving.
		})
	}
}
