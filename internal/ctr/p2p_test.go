package ctr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
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
	deliveries     uint64
}
type roomFixtureEvent struct {
	event companion.Event
	after chan struct{}
}
type roomFixtureTransport struct {
	hub     *roomFixtureHub
	pid     string
	events  chan companion.Event
	queued  chan roomFixtureEvent
	next    chan struct{}
	done    chan struct{}
	stopped chan struct{}
}

// Returning to Events proves that the real consumer finished receive for the
// preceding event. A queued marker can therefore acknowledge a processing
// boundary without adding a test hook to the production backend.
func (t *roomFixtureTransport) Events() <-chan companion.Event {
	select {
	case t.next <- struct{}{}:
	case <-t.done:
	}
	return t.events
}
func (t *roomFixtureTransport) dispatch() {
	defer close(t.stopped)
	var completed chan struct{}
	var item roomFixtureEvent
	var pending bool
	for {
		input, output := t.queued, (chan companion.Event)(nil)
		if pending {
			input, output = nil, t.events
		}
		// Always service Events reentry, including while the queue is empty or
		// an event is waiting for delivery. Only a completed send arms its marker.
		select {
		case <-t.next:
			if completed != nil {
				close(completed)
				completed = nil
			}
		case item = <-input:
			pending = true
		case output <- item.event:
			completed = item.after
			pending = false
		case <-t.done:
			return
		}
	}
}

func TestP2PFixtureEventsRemainPromptWithoutMessages(t *testing.T) {
	transport := &roomFixtureTransport{events: make(chan companion.Event), queued: make(chan roomFixtureEvent, 1), next: make(chan struct{}), done: make(chan struct{}), stopped: make(chan struct{})}
	go transport.dispatch()
	t.Cleanup(func() { close(transport.done); <-transport.stopped })
	wake := make(chan struct{}, 1)
	returned := make(chan bool, 1)
	go func() {
		for i := 0; i < 4; i++ {
			wake <- struct{}{}
			// A timer/cancellation branch may win without consuming an event.
			// Reevaluating Events must still let that independent branch run.
			select {
			case <-transport.Events():
				returned <- false
				return
			case <-wake:
			}
		}
		returned <- true
	}()
	select {
	case ok := <-returned:
		if !ok {
			t.Fatal("empty fixture queue fabricated an event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("repeated Events calls blocked an independent wakeup")
	}
	done := make(chan struct{})
	transport.queued <- roomFixtureEvent{event: companion.Event{Event: "fixture.barrier"}, after: done}
	// Repeated evaluations before receiving the marker must not acknowledge it.
	stream := transport.Events()
	transport.Events()
	select {
	case <-done:
		t.Fatal("barrier completed before its event was delivered")
	default:
	}
	select {
	case <-stream:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture marker was not delivered")
	}
	select {
	case <-done:
		t.Fatal("barrier completed before the consumer returned")
	default:
	}
	transport.Events()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("delivered marker was not acknowledged on consumer return")
	}
	transport.Events() // An acknowledged marker must not be closed twice.
}

// The caller holds h.mu, preserving pipe order for every recipient.
func (h *roomFixtureHub) emit(pid string, event companion.Event) {
	h.deliveries++
	h.clients[pid].queued <- roomFixtureEvent{event: event}
}

func settleRoomEvents(t *testing.T, h *roomFixtureHub) {
	t.Helper()
	h.mu.Lock()
	ids := make([]string, 0, len(h.clients))
	for id := range h.clients {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	h.mu.Unlock()
	for round := 0; round < 8; round++ {
		h.mu.Lock()
		before := h.deliveries
		h.mu.Unlock()
		for _, id := range ids {
			h.mu.Lock()
			transport := h.clients[id]
			h.mu.Unlock()
			done := make(chan struct{})
			transport.queued <- roomFixtureEvent{event: companion.Event{Event: "fixture.barrier"}, after: done}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("room event barrier did not complete")
			}
		}
		h.mu.Lock()
		unchanged := h.deliveries == before
		h.mu.Unlock()
		if unchanged {
			return
		}
	}
	t.Fatal("room events did not quiesce across processing barriers")
}

func (t *roomFixtureTransport) Done() <-chan struct{} { return t.done }
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
			h.emit(pid, companion.Event{Event: "room.changed", RoomID: id, Owner: room.Owner, Members: append([]string(nil), room.Members...)})
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
					h.emit(pid, companion.Event{Event: "room.closed", RoomID: id})
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
				h.emit(pid, companion.Event{Event: "room.changed", RoomID: id, Owner: room.Owner, Members: members})
			}
		}
	case "peer.send":
		peer := args["peer"].(string)
		if !containsMember(room.Members, peer) || !containsMember(room.Members, t.pid) {
			return errors.New("not member")
		}
		h.emit(peer, companion.Event{Event: "peer.message", RoomID: id, Sender: t.pid, Payload: args["payload"].(string)})
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

func newRoomFixture(t *testing.T, hub *roomFixtureHub, pid uint64, decorate ...func(RoomTransport) RoomTransport) (*P2PBackend, *roomFixturePeer) {
	return newTimedRoomFixture(t, hub, pid, nil, decorate...)
}

func newTimedRoomFixture(t *testing.T, hub *roomFixtureHub, pid uint64, timing *P2PTiming, decorate ...func(RoomTransport) RoomTransport) (*P2PBackend, *roomFixturePeer) {
	t.Helper()
	id := strconv.FormatUint(pid, 10)
	transport := &roomFixtureTransport{hub: hub, pid: id, events: make(chan companion.Event), queued: make(chan roomFixtureEvent, 256), next: make(chan struct{}), done: make(chan struct{}), stopped: make(chan struct{})}
	hub.mu.Lock()
	hub.clients[id] = transport
	hub.mu.Unlock()
	go transport.dispatch()
	peer := &roomFixturePeer{testPeer: &testPeer{N: pid, Player: &platform.PlayerID{PID: pid, Title: 5760}}, pushes: make(chan capturedPush, 32)}
	m := NewMatchmaker(func() []uint64 { return nil })
	m.Clock = func() time.Time { return time.Unix(1000, 0) }
	h := &asyncHandler{ReplyCounter: &lobby.ReplyCounter{}, matchmaker: m, info: map[uint64]string{}}
	state := presence.NewStore(nil)
	contextHandler := &contextHandler{&lobby.ContextHandler{ReplyCounter: h.ReplyCounter, ReadInfo: ReadInfo, Registry: lobby.NewSessionRegistry(), Presence: state, Relay: relay.NewTable(&diagnostics.Diagnostics{RedactLogs: true}, "127.0.0.1", "")}}
	table := lobby.NewServiceTable(h.ReplyCounter, map[byte]lobby.TaskHandler{145: h, 138: contextHandler, 12: &utilityHandler{&presence.Handler{Replies: h.ReplyCounter, Presence: state}}}, nil)
	var roomTransport RoomTransport = transport
	for _, wrap := range decorate {
		roomTransport = wrap(roomTransport)
	}
	b, err := InstallP2P(context.Background(), table, roomTransport, pid, netip.AddrPort{}, timing)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(); close(transport.done); b.Wait(); <-transport.stopped })
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

func parityRoomInfo(pid uint64) string {
	var info PlayerInfo
	_ = json.Unmarshal([]byte(roomInfo()), &info)
	credential := make([]byte, 16)
	for i := range credential {
		credential[i] = byte(pid) + byte(i)
	}
	info.Listen.ID = base64.StdEncoding.EncodeToString(credential[:8])
	info.Listen.Key = base64.StdEncoding.EncodeToString(credential)
	raw, _ := json.Marshal(info)
	return string(raw)
}

func centralParitySearch(t *testing.T, m *matchFixture, pid uint64) *Search {
	t.Helper()
	s := testCTRSearchFor(m, t, pid, 0)
	if json.Unmarshal([]byte(parityRoomInfo(pid)), &s.info) != nil {
		t.Fatal("invalid parity player information")
	}
	return s
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
	if e1 != nil || e2 != nil || len(r.Remaining()) != 0 {
		t.Fatal("invalid push docs")
	}
	return ids, h, b
}

func assertRoomNotice(t *testing.T, recipient *roomFixturePeer, push capturedPush, expected notice) {
	t.Helper()
	gotIDs, gotHost, gotBackend := pushDocuments(t, push)
	if expected.Conn.PID() != recipient.PID() || push.event != expected.Event || !reflect.DeepEqual(gotIDs, expected.IDs) {
		t.Fatal("P2P notice recipient, event or IDs differ from central")
	}
	decode := func(raw string) map[string]any {
		var doc map[string]any
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		if d.Decode(&doc) != nil {
			t.Fatal("invalid host document")
		}
		return doc
	}
	want, got := decode(expected.Host), decode(gotHost)
	wantListen, wantOK := want["listen_server"].(map[string]any)
	gotListen, gotOK := got["listen_server"].(map[string]any)
	if !wantOK || !gotOK {
		t.Fatal("missing listen_server")
	}
	if wantListen["host_player_id"] != json.Number(strconv.FormatUint(recipient.PID(), 10)) {
		address, ok := wantListen["host_address"].(string)
		if !ok {
			t.Fatal("missing central host address")
		}
		proxy, err := rewriteCommonAddress(address, "127.0.0.90", 43074)
		if err != nil || gotListen["host_address"] != proxy {
			t.Fatal("member received an incorrect local proxy address")
		}
		// Only this field is recipient-specific. Host PID and every other host
		// field, plus the complete backend document including DTLS, must match.
		gotListen["host_address"] = wantListen["host_address"]
	}
	if !reflect.DeepEqual(got, want) || gotBackend != expected.Backend {
		t.Fatal("P2P notice documents differ from central")
	}
}

// Call after settleRoomEvents. Once every queued event has completed, draining
// each recipient proves both its exact sequence and the absence of extra pushes.
func assertRoomNoticeQueues(t *testing.T, peers []*roomFixturePeer, expected []notice) {
	t.Helper()
	for _, n := range expected {
		if !slices.ContainsFunc(peers, func(p *roomFixturePeer) bool { return p.PID() == n.Conn.PID() }) {
			t.Fatal("central notice has an unobserved recipient")
		}
	}
	for _, peer := range peers {
		for _, n := range expected {
			if n.Conn.PID() == peer.PID() {
				select {
				case push := <-peer.pushes:
					assertRoomNotice(t, peer, push, n)
				default:
					t.Fatal("missing recipient notice after room event barrier")
				}
			}
		}
		select {
		case <-peer.pushes:
			t.Fatal("unexpected additional recipient notice")
		default:
		}
	}
}

func assertRoomRequestsSettled(t *testing.T, backends ...*P2PBackend) {
	t.Helper()
	for _, b := range backends {
		b.mu.Lock()
		settled := len(b.pending) == 0 && len(b.acks) == 0
		b.mu.Unlock()
		if !settled {
			t.Fatal("room requests or acknowledgement timers remain after barrier")
		}
	}
}

func TestP2PEngineMatchesCentralNotices(t *testing.T) {
	hub := newFixtureHub()
	a, pa := newRoomFixture(t, hub, 101)
	b, pb := newRoomFixture(t, hub, 102)
	deliverResult(t, a.Handle(pa, taskRequest(2, stringArgs(parityRoomInfo(pa.PID())))))
	deliverResult(t, a.Handle(pa, taskRequest(18, searchArgs(pa.PID()))))
	deliverResult(t, b.Handle(pb, taskRequest(2, stringArgs(parityRoomInfo(pb.PID())))))
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
	sa, sb := centralParitySearch(t, &m, 101), centralParitySearch(t, &m, 102)
	m.Enqueue(sa)
	centralCreate := m.Enqueue(sb)[0]
	_, notices := m.SyncDoc(sa.Conn, centralCreate.IDs[1], 1, centralCreate.Host)
	assertRoomNotice(t, pa, created, centralCreate)
	assertRoomNotice(t, pb, joined, notices[0])
	settleRoomEvents(t, hub)
	assertRoomRequestsSettled(t, a, b)
	assertRoomNoticeQueues(t, []*roomFixturePeer{pa, pb}, nil)
	// Owner loss invalidates the room; no automatic engine migration occurs.
	a.leaveRoom()
	settleRoomEvents(t, hub)
	b.mu.Lock()
	gone := b.room.ID == ""
	b.mu.Unlock()
	if !gone {
		t.Fatal("owner loss kept stale room")
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
		deliverResult(t, fixture.backend.Handle(fixture.peer, taskRequest(2, stringArgs(parityRoomInfo(fixture.peer.PID())))))
		raw, _ := demonware.NewReader(searchArgs(fixture.peer.PID())).String()
		search, err := NewMatchmaker(nil).ParseSearch(fixture.peer, raw, parityRoomInfo(fixture.peer.PID()))
		if err != nil || fixture.backend.selectRoom(search) != nil {
			t.Fatal("fixture directory selection")
		}
	}
	settleRoomEvents(t, hub)
	a.mu.Lock()
	count := len(a.room.Members)
	a.mu.Unlock()
	if count != 2 {
		t.Fatal("follower not observed")
	}
	central := matchFixture{Matchmaker: *NewMatchmaker(func() []uint64 { return nil })}
	central.Clock = func() time.Time { return time.Unix(1000, 0) }
	leader := centralParitySearch(t, &central, 101)
	leader.Doc.Members = []uint64{101, 102}
	leader.Seats = 2
	partyArgs := stringArgs(`{"members":[101,102],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":0,"matchmaking_version":200}}}`)
	deliverResult(t, a.Handle(pa, taskRequest(18, partyArgs)))
	settleRoomEvents(t, hub)
	assertRoomNoticeQueues(t, []*roomFixturePeer{pa, pb}, central.Enqueue(leader))
	// A follower never sent a room RPC or owned a search. Its departure must
	// nevertheless remove the seat claimed by the leader, as the central engine does.
	b.CloseSession(pb)
	follower := testCTRSearchFor(&central, t, 102, 0).Conn
	notices := central.Leave(follower, 0)
	if len(notices) != 1 || notices[0].Event != EventUpdateLobby {
		t.Fatal("central follower departure reference did not update the leader")
	}
	settleRoomEvents(t, hub)
	assertRoomRequestsSettled(t, a, b)
	assertRoomNoticeQueues(t, []*roomFixturePeer{pa, pb}, notices)
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
			if len(central.Enqueue(ca)) != 0 || len(central.Enqueue(cb)) != 0 {
				t.Fatal("central waiting searches unexpectedly emitted a notice")
			}
			// Waiting searches emit no notices for either client. Complete the
			// remote reply/ack before cancellation, then compare that empty trace.
			settleRoomEvents(t, hub)
			assertRoomRequestsSettled(t, a, b)
			assertRoomNoticeQueues(t, []*roomFixturePeer{pa, pb}, nil)
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
			settleRoomEvents(t, hub)
			assertRoomRequestsSettled(t, a, b)
			assertRoomNoticeQueues(t, []*roomFixturePeer{pa, pb}, nil)
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
			if !memberLeft || waiting != expected {
				t.Fatal("cancel did not settle room membership")
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

type membershipSnapshotRace struct {
	RoomTransport
	after func(directoryRoom)
}

func (r *membershipSnapshotRace) Call(ctx context.Context, op string, args map[string]any, out any) error {
	err := r.RoomTransport.Call(ctx, op, args, out)
	if err == nil && op == "directory.members" {
		r.after(*out.(*directoryRoom))
	}
	return err
}

func TestP2PMembershipEventWinsOverOlderSnapshot(t *testing.T) {
	hub := newFixtureHub()
	var backend *P2PBackend
	calls := 0
	backend, peer := newRoomFixture(t, hub, 101, func(transport RoomTransport) RoomTransport {
		return &membershipSnapshotRace{transport, func(snapshot directoryRoom) {
			calls++
			if calls > 1 {
				return
			}
			// The pipe can wake the event consumer before the goroutine waiting
			// on an older directory.members reply is scheduled again.
			members := []string{"101", "102"}
			hub.mu.Lock()
			room := hub.rooms[snapshot.ID]
			room.Members = members
			hub.rooms[snapshot.ID] = room
			hub.mu.Unlock()
			backend.receive(companion.Event{Event: "room.changed", RoomID: snapshot.ID, Owner: "101", Members: members})
		}}
	})
	deliverResult(t, backend.Handle(peer, taskRequest(2, stringArgs(roomInfo()))))
	search := backend.Handle(peer, taskRequest(18, searchArgs(peer.PID())))
	if !successfulReply(search.Reply) || calls != 2 {
		t.Fatal("racing membership did not refresh before admission")
	}
	deliverResult(t, search)
	backend.mu.Lock()
	preserved := containsMember(backend.room.Members, "102")
	backend.mu.Unlock()
	if !preserved {
		t.Fatal("older membership snapshot overwrote a newer directory event")
	}
}

func TestP2PRepeatedMembershipChangesFailClosed(t *testing.T) {
	hub := newFixtureHub()
	var backend *P2PBackend
	calls := 0
	backend, peer := newRoomFixture(t, hub, 101, func(transport RoomTransport) RoomTransport {
		return &membershipSnapshotRace{transport, func(snapshot directoryRoom) {
			calls++
			backend.receive(companion.Event{Event: "room.changed", RoomID: snapshot.ID, Owner: "101", Members: []string{"101"}})
		}}
	})
	deliverResult(t, backend.Handle(peer, taskRequest(2, stringArgs(roomInfo()))))
	search := backend.Handle(peer, taskRequest(18, searchArgs(peer.PID())))
	if successfulReply(search.Reply) || calls != 3 {
		t.Fatal("unstable membership was admitted or retried without a bound")
	}
	backend.mu.Lock()
	left := backend.room.ID == ""
	backend.mu.Unlock()
	hub.mu.Lock()
	left = left && len(hub.rooms) == 0
	hub.mu.Unlock()
	if !left {
		t.Fatal("failed selection retained directory membership")
	}
}
