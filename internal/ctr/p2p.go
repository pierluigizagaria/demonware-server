package ctr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/companion"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/strictjson"
)

const roomPayloadLimit = 32768

// RoomTransport is implemented by the inherited library pipe. It never carries
// arbitrary publisher tasks: P2PBackend owns the narrower room allow-list.
type RoomTransport interface {
	Call(context.Context, string, map[string]any, any) error
	Events() <-chan companion.Event
	Done() <-chan struct{}
}

type directoryRoom struct {
	ID       string         `json:"room_id"`
	Owner    string         `json:"owner"`
	Members  []string       `json:"members"`
	Metadata map[string]any `json:"metadata"`
}

type roomPacket struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	PID     string `json:"pid"`
	Conn    string `json:"conn"`
	Task    byte   `json:"task,omitempty"`
	Event   uint32 `json:"event,omitempty"`
	Data    []byte `json:"data,omitempty"`
	OK      bool   `json:"ok,omitempty"`
}

type roomAck struct {
	peer   string
	conn   string
	after  func(error)
	timer  *time.Timer
	timing *timingSpan
}
type roomRate struct {
	at    time.Time
	count int
}

// P2PBackend leaves the engine unchanged. One directory owner executes room
// tasks; every other publisher still serves its own economy and statistics.
type P2PBackend struct {
	ctx                context.Context
	cancel             context.CancelFunc
	transport          RoomTransport
	engine             *asyncHandler
	owner              uint64
	ownerText          string
	canonical          netip.AddrPort
	mu                 sync.Mutex
	selectMu           sync.Mutex
	mutationMu         sync.Mutex // serializes engine mutations with room invalidation
	generation         uint64
	membershipRevision uint64
	room               directoryRoom
	filter             string
	local              lobby.TaskSession
	remotes            map[string]*remoteRoomPeer
	pending            map[string]chan roomPacket
	acks               map[string]*roomAck
	rates              map[string]roomRate
	ids                atomic.Uint64
	remoteIDs          atomic.Uint64
	workers            sync.WaitGroup
	ackWorkers         sync.WaitGroup
	activeRoom         uint64
	advertisement      *friendAdvertisementHandler
	timing             *P2PTiming
}

func InstallP2P(ctx context.Context, table *lobby.ServiceTable, transport RoomTransport, owner uint64, canonical netip.AddrPort, timing *P2PTiming) (*P2PBackend, error) {
	engine, ok := table.Handlers[SvcAsyncMatchMaking].(*asyncHandler)
	if !ok || transport == nil || owner == 0 || (canonical.IsValid() && !validCanonicalEndpoint(canonical)) {
		return nil, errors.New("invalid P2P dependencies")
	}
	ctx, cancel := context.WithCancel(ctx)
	if timing != nil {
		transport = &timedRoomTransport{RoomTransport: transport, timing: timing}
	}
	b := &P2PBackend{ctx: ctx, cancel: cancel, transport: transport, engine: engine, owner: owner, ownerText: strconv.FormatUint(owner, 10), remotes: map[string]*remoteRoomPeer{}, pending: map[string]chan roomPacket{}, acks: map[string]*roomAck{}, rates: map[string]roomRate{}}
	b.canonical = canonical
	b.timing = timing
	table.Handlers[SvcAsyncMatchMaking] = b
	if fallback := table.Handlers[SvcTitleUtilities]; fallback != nil {
		table.Handlers[SvcTitleUtilities] = &localNameHandler{backend: b, fallback: fallback}
	}
	if fallback, ok := table.Handlers[138].(*contextHandler); ok {
		b.advertisement = &friendAdvertisementHandler{backend: b, fallback: fallback}
		table.Handlers[138] = b.advertisement
	}
	previousClose := table.OnCloseSession
	table.OnCloseSession = func(peer lobby.TaskSession) {
		b.CloseSession(peer)
		if previousClose != nil {
			previousClose(peer)
		}
	}
	b.workers.Add(1)
	go func() { defer b.workers.Done(); b.events() }()
	return b, nil
}

func (b *P2PBackend) Close() { b.cancel() }
func (b *P2PBackend) Wait()  { b.workers.Wait(); b.ackWorkers.Wait() }

func roomTask(task byte) bool {
	switch task {
	case TaskSetPlayerInfo, 8, TaskLobbyDisbanded, TaskGetLobbyDocuments, TaskSyncLobbyDocuments, TaskStartSearch:
		return true
	}
	return false
}

func (b *P2PBackend) failure(task byte) lobby.TaskResult {
	return lobby.TaskResult{Reply: b.engine.TaskReply(task, lobby.ErrUnhandled, nil)}
}

func (b *P2PBackend) handle(peer lobby.TaskSession, request lobby.TaskRequest) lobby.TaskResult {
	if peer.PID() != b.owner || request.Service != SvcAsyncMatchMaking {
		return b.failure(request.Task)
	}
	args := append([]byte(nil), request.Args.Remaining()...)
	if len(args) > 16384 {
		return b.failure(request.Task)
	}
	b.mu.Lock()
	if b.local != nil && peer.ConnID() < b.local.ConnID() {
		b.mu.Unlock()
		return b.failure(request.Task)
	}
	previous := b.local
	b.mu.Unlock()
	if previous != nil && previous.ConnID() != peer.ConnID() {
		b.leaveRoom()
	}
	b.mu.Lock()
	b.local = peer
	b.mu.Unlock()
	// Registration stays local until a room has been selected. The exact copied
	// player information is then sent only to the selected room's owner.
	if request.Task == TaskSetPlayerInfo {
		if !validPlayerInfoArgs(args) {
			return b.failure(request.Task)
		}
		return b.engine.Handle(peer, taskRequest(request.Task, args))
	}
	if !roomTask(request.Task) {
		switch request.Task {
		case TaskGetPlayerToken, TaskQoSHostsReply, TaskGetMMStatus, TaskInitMatchMaking, TaskStartMatchMaking, TaskAckExpectGame, TaskInitiateDCQoS:
			return b.engine.Handle(peer, taskRequest(request.Task, args))
		}
		return b.failure(request.Task)
	}
	if request.Task == TaskStartSearch {
		raw, err := demonware.NewReader(args).String()
		if err != nil {
			return b.failure(request.Task)
		}
		search, err := NewMatchmaker(nil).ParseSearch(peer, raw, b.engine.PlayerInfo(peer.ConnID()))
		if err != nil {
			return b.failure(request.Task)
		}
		if err = b.selectRoom(search); err != nil {
			return b.failure(request.Task)
		}
	}
	b.mu.Lock()
	room := b.room
	generation := b.generation
	b.mu.Unlock()
	if room.ID == "" { // Nothing remote exists; preserve local cancellation semantics.
		return b.engine.Handle(peer, taskRequest(request.Task, args))
	}
	if request.Task == TaskStartSearch && !b.validSearchMembers(args, room) {
		return b.failure(request.Task)
	}
	var result lobby.TaskResult
	if room.Owner == b.ownerText {
		b.mutationMu.Lock()
		b.mu.Lock()
		current := b.room.ID == room.ID && b.generation == generation
		b.mu.Unlock()
		if !current {
			b.mutationMu.Unlock()
			return b.failure(request.Task)
		}
		result = b.engine.Handle(&localRoomPeer{TaskSession: peer, backend: b}, taskRequest(request.Task, args))
		b.mutationMu.Unlock()
	} else {
		if request.Task == TaskStartSearch {
			w := &demonware.Writer{}
			w.String(b.engine.PlayerInfo(peer.ConnID()))
			registration, err := b.remoteCall(peer, room, TaskSetPlayerInfo, w.Bytes())
			if err != nil {
				return b.failure(request.Task)
			}
			if registration.AfterReply != nil {
				registration.AfterReply(nil)
			}
			if !successfulReply(registration.Reply) {
				return b.failure(request.Task)
			}
		}
		var err error
		result, err = b.remoteCall(peer, room, request.Task, args)
		if err != nil {
			return b.failure(request.Task)
		}
	}
	if request.Task == TaskGetLobbyDocuments {
		var err error
		result.Reply, err = b.rewriteReply(result.Reply)
		if err != nil {
			if result.AfterReply != nil {
				result.AfterReply(err)
			}
			return b.failure(request.Task)
		}
	}
	leave := request.Task == 8 && replyBool(result.Reply)
	if request.Task == TaskLobbyDisbanded {
		id, err := demonware.NewReader(args).Uint64()
		b.mu.Lock()
		leave = err == nil && id != 0 && id == b.activeRoom
		b.mu.Unlock()
	}
	after := result.AfterReply
	result.AfterReply = func(err error) {
		b.mutationMu.Lock()
		b.mu.Lock()
		current := b.room.ID == room.ID && b.generation == generation
		b.mu.Unlock()
		if !current {
			b.mutationMu.Unlock()
			return // clearRoom already discarded pending engine work for this room.
		}
		if after != nil {
			after(err)
		}
		if leave {
			b.clearRoomLocked()
		}
		b.mutationMu.Unlock()
		if leave {
			_ = b.transport.Call(b.ctx, "directory.leave", map[string]any{"room_id": room.ID}, nil)
		} else if room.Owner == b.ownerText {
			b.updateDirectory()
		}
	}
	return result
}

func taskRequest(task byte, args []byte) lobby.TaskRequest {
	return lobby.TaskRequest{Service: SvcAsyncMatchMaking, Task: task, Args: demonware.NewReader(args), Payload: args}
}

func successfulReply(raw []byte) bool {
	r := demonware.NewReader(raw)
	_, e1 := r.Uint64()
	code, e2 := r.Uint32()
	return e1 == nil && e2 == nil && code == 0
}

func replyBool(raw []byte) bool {
	r := demonware.NewReader(raw)
	_, e1 := r.Uint64()
	code, e2 := r.Uint32()
	_, e3 := r.Uint8()
	count, e4 := r.Uint32()
	_, e5 := r.Uint32()
	value, e6 := r.Bool()
	return e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil && code == 0 && count == 1 && value
}

func validPlayerInfoArgs(args []byte) bool {
	r := demonware.NewReader(args)
	raw, err := r.String()
	if err != nil || len(raw) > 8192 {
		return false
	}
	var fields map[string]json.RawMessage
	var info PlayerInfo
	if !strictjson.Decode(raw, &fields) || json.Unmarshal([]byte(raw), &info) != nil {
		return false
	}
	for encoded, size := range map[string]int{info.Listen.Address: 67, info.Listen.ID: 8, info.Listen.Key: 16} {
		raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(raw) != size {
			return false
		}
	}
	return true
}

func (b *P2PBackend) selectRoom(search *Search) (selectionError error) {
	b.selectMu.Lock()
	defer b.selectMu.Unlock()
	sum := sha256.Sum256([]byte(search.Filter))
	filter := hex.EncodeToString(sum[:])
	b.mu.Lock()
	current := b.room
	same := b.filter == filter
	b.mu.Unlock()
	if current.ID != "" && same {
		return nil
	}
	if current.ID != "" {
		b.leaveRoom()
	}
	b.mu.Lock()
	selectionGeneration := b.generation
	b.mu.Unlock()
	metadata := map[string]any{"playlist": filter, "type": "ctr-async-1"}
	var found struct {
		Rooms []directoryRoom `json:"rooms"`
	}
	if err := b.transport.Call(b.ctx, "directory.search", map[string]any{"metadata": metadata, "limit": 32}, &found); err != nil {
		return err
	}
	if len(found.Rooms) > 32 {
		return errors.New("invalid room directory")
	}
	var room directoryRoom
	for _, candidate := range found.Rooms {
		// Steam exposes public owner metadata before joining, not membership.
		if !validDecimal(candidate.ID) || !validDecimal(candidate.Owner) || candidate.Owner == b.ownerText || candidate.Metadata["playlist"] != filter || candidate.Metadata["type"] != "ctr-async-1" {
			continue
		}
		var joined directoryRoom
		if b.transport.Call(b.ctx, "directory.join", map[string]any{"room_id": candidate.ID}, &joined) == nil {
			room = joined
			break
		}
	}
	if room.ID == "" {
		metadata["free_slots"] = search.Doc.Slots.Max - search.Seats
		if err := b.transport.Call(b.ctx, "directory.create", map[string]any{"metadata": metadata, "capacity": search.Doc.Slots.Max}, &room); err != nil {
			return err
		}
	}
	// Any failure after a successful library join/create must undo membership.
	defer func() {
		if selectionError == nil || !validDecimal(room.ID) {
			return
		}
		b.mutationMu.Lock()
		b.mu.Lock()
		selected := b.room.ID == room.ID
		b.mu.Unlock()
		if selected {
			b.clearRoomLocked()
		}
		b.mutationMu.Unlock()
		_ = b.transport.Call(b.ctx, "directory.leave", map[string]any{"room_id": room.ID}, nil)
	}()
	if !validDirectoryRoom(room) || !containsMember(room.Members, b.ownerText) || room.Metadata["playlist"] != filter || room.Metadata["type"] != "ctr-async-1" {
		return errors.New("invalid selected room")
	}
	b.mutationMu.Lock()
	b.mu.Lock()
	if b.generation != selectionGeneration || b.local == nil || b.local.ConnID() != search.Conn.ConnID() || b.ctx.Err() != nil {
		b.mu.Unlock()
		b.mutationMu.Unlock()
		return errors.New("selection was cancelled")
	}
	b.room = room
	b.filter = filter
	b.mu.Unlock()
	b.mutationMu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		b.mu.Lock()
		revision := b.membershipRevision
		b.mu.Unlock()
		var currentMembers directoryRoom
		if err := b.transport.Call(b.ctx, "directory.members", map[string]any{"room_id": room.ID}, &currentMembers); err != nil {
			return err
		}
		if !validDirectoryRoom(currentMembers) || currentMembers.ID != room.ID || currentMembers.Owner != room.Owner || !containsMember(currentMembers.Members, b.ownerText) {
			return errors.New("room membership changed during selection")
		}
		b.mu.Lock()
		if b.room.ID != room.ID || b.generation != selectionGeneration {
			b.mu.Unlock()
			return errors.New("room closed during selection")
		}
		if b.membershipRevision != revision {
			// A directory event raced with the RPC. Its ordering against this
			// snapshot is unknown, so refresh instead of restoring stale members.
			b.mu.Unlock()
			continue
		}
		b.room.Members = append([]string(nil), currentMembers.Members...)
		b.mu.Unlock()
		return nil
	}
	return errors.New("room membership did not stabilize during selection")
}

func validDecimal(s string) bool {
	id, err := strconv.ParseUint(s, 10, 64)
	return err == nil && id != 0 && strconv.FormatUint(id, 10) == s
}
func containsMember(members []string, pid string) bool {
	for _, p := range members {
		if p == pid {
			return true
		}
	}
	return false
}
func validDirectoryRoom(r directoryRoom) bool {
	if !validDecimal(r.ID) || !validDecimal(r.Owner) || len(r.Members) == 0 || len(r.Members) > 16 || !containsMember(r.Members, r.Owner) {
		return false
	}
	seen := map[string]bool{}
	for _, p := range r.Members {
		if !validDecimal(p) || seen[p] {
			return false
		}
		seen[p] = true
	}
	return true
}

func (b *P2PBackend) validSearchMembers(args []byte, room directoryRoom) bool {
	raw, err := demonware.NewReader(args).String()
	var doc SearchDoc
	var fields map[string]json.RawMessage
	if err != nil || !strictjson.Decode(raw, &fields) || json.Unmarshal([]byte(raw), &doc) != nil {
		return false
	}
	for _, pid := range doc.Members {
		if !containsMember(room.Members, strconv.FormatUint(pid, 10)) {
			return false
		}
	}
	return true
}

func (b *P2PBackend) send(room, peer string, packet roomPacket) error {
	raw, err := json.Marshal(packet)
	if err != nil || len(raw) > roomPayloadLimit {
		return errors.New("room payload capacity")
	}
	return b.transport.Call(b.ctx, "peer.send", map[string]any{"room_id": room, "peer": peer, "payload": base64.StdEncoding.EncodeToString(raw)}, nil)
}

func (b *P2PBackend) remoteCall(peer lobby.TaskSession, room directoryRoom, task byte, args []byte) (result lobby.TaskResult, err error) {
	span := b.timing.begin(timingRemote, "", uint32(task))
	defer func() {
		if span == nil {
			return
		}
		if err == nil && !successfulReply(result.Reply) {
			span.end(timingRefused)
		} else {
			span.endError(err)
		}
	}()
	id := strconv.FormatUint(b.ids.Add(1), 10)
	conn := strconv.FormatUint(peer.ConnID(), 10)
	ch := make(chan roomPacket, 1)
	b.mu.Lock()
	if b.room.ID != room.ID || b.room.Owner != room.Owner || len(b.pending) >= 32 {
		b.mu.Unlock()
		return lobby.TaskResult{}, errors.New("room request capacity")
	}
	b.pending[id] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, id); b.mu.Unlock() }()
	if err := b.send(room.ID, room.Owner, roomPacket{Version: 1, Kind: "request", ID: id, PID: b.ownerText, Conn: conn, Task: task, Data: args}); err != nil {
		return lobby.TaskResult{}, err
	}
	timer := time.NewTimer(companion.RequestLimit)
	defer timer.Stop()
	select {
	case response := <-ch:
		if response.Conn != conn || response.Task != task || len(response.Data) > 16384 {
			return lobby.TaskResult{}, errors.New("room response mismatch")
		}
		return lobby.TaskResult{Reply: response.Data, AfterReply: func(sendErr error) {
			_ = b.send(room.ID, room.Owner, roomPacket{Version: 1, Kind: "ack", ID: id, PID: b.ownerText, Conn: conn, OK: sendErr == nil})
		}}, nil
	case <-timer.C:
		span.end(timingTimeout)
		return lobby.TaskResult{}, errors.New("room request timeout")
	case <-b.ctx.Done():
		return lobby.TaskResult{}, b.ctx.Err()
	case <-b.transport.Done():
		return lobby.TaskResult{}, errors.New("room transport closed")
	}
}

func (b *P2PBackend) events() {
	defer b.clearRoom()
	for {
		select {
		case e := <-b.transport.Events():
			b.receive(e)
		case <-b.ctx.Done():
			return
		case <-b.transport.Done():
			return
		}
	}
}

func (b *P2PBackend) receive(e companion.Event) {
	b.mutationMu.Lock()
	defer b.mutationMu.Unlock()
	if e.Event == "room.selected" {
		room := directoryRoom{ID: e.RoomID, Owner: e.Owner, Members: e.Members, Metadata: e.Metadata}
		filter, ok := e.Metadata["playlist"].(string)
		if !validDirectoryRoom(room) || !containsMember(room.Members, b.ownerText) || !ok || len(filter) != 64 || e.Metadata["type"] != "ctr-async-1" {
			return
		}
		if _, err := hex.DecodeString(filter); err != nil {
			return
		}
		b.clearRoomLocked()
		b.mu.Lock()
		b.room = room
		b.filter = filter
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	room := b.room
	b.mu.Unlock()
	if room.ID == "" || e.RoomID != room.ID {
		return
	}
	switch e.Event {
	case "room.closed":
		b.clearRoomLocked()
		return
	case "room.changed":
		updated := directoryRoom{ID: room.ID, Owner: e.Owner, Members: e.Members}
		if !validDirectoryRoom(updated) || e.Owner != room.Owner || !containsMember(e.Members, b.ownerText) {
			b.clearRoomLocked()
			return
		}
		b.mu.Lock()
		b.room.Members = append([]string(nil), e.Members...)
		b.membershipRevision++
		var dropped []*remoteRoomPeer
		for _, pid := range room.Members {
			if containsMember(e.Members, pid) {
				continue
			}
			peer := b.remotes[pid]
			delete(b.remotes, pid)
			if peer == nil {
				// A party follower owns no search and may never send an RPC.
				// Its authenticated directory departure still releases its seat.
				id, _ := strconv.ParseUint(pid, 10, 64)
				peer = &remoteRoomPeer{backend: b, pid: id, id: (uint64(1) << 63) | b.remoteIDs.Add(1)}
			}
			dropped = append(dropped, peer)
		}
		b.mu.Unlock()
		for _, peer := range dropped {
			b.engine.forget(peer.ConnID())
			SendNotices(b.engine.matchmaker.Leave(peer, 0))
		}
		b.updateDirectory()
		return
	case "peer.message":
	default:
		return
	}
	if !containsMember(room.Members, e.Sender) || e.Sender == b.ownerText || !b.takeRate(e.Sender) {
		return
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(e.Payload)
	if err != nil || len(raw) > roomPayloadLimit {
		return
	}
	var packet roomPacket
	if !strictjson.Decode(string(raw), &packet) || packet.Version != 1 || packet.PID != e.Sender || !validDecimal(packet.Conn) || len(packet.Data) > 16384 {
		return
	}
	switch packet.Kind {
	case "request":
		if room.Owner != b.ownerText || !roomTask(packet.Task) || !validDecimal(packet.ID) {
			return
		}
		if packet.Task == TaskSetPlayerInfo && !validPlayerInfoArgs(packet.Data) {
			return
		}
		if packet.Task == TaskStartSearch && !b.validSearchMembers(packet.Data, room) {
			return
		}
		peer := b.remotePeer(e.Sender, packet.Conn)
		if peer == nil {
			return
		}
		requestID, _ := strconv.ParseUint(packet.ID, 10, 64)
		b.mu.Lock()
		if requestID <= peer.lastRequest || len(b.acks) >= 64 {
			b.mu.Unlock()
			return
		}
		peer.lastRequest = requestID
		b.mu.Unlock()
		result := b.engine.Handle(peer, taskRequest(packet.Task, packet.Data))
		key := e.Sender + ":" + packet.ID
		ack := &roomAck{peer: e.Sender, conn: packet.Conn, after: result.AfterReply, timing: b.timing.begin(timingAck, "", uint32(packet.Task))}
		b.mu.Lock()
		if len(b.acks) >= 64 || b.acks[key] != nil {
			b.mu.Unlock()
			ack.timing.end(timingError)
			if result.AfterReply != nil {
				result.AfterReply(errors.New("room acknowledgement capacity"))
			}
			return
		}
		b.acks[key] = ack
		b.ackWorkers.Add(1)
		ack.timer = time.AfterFunc(companion.RequestLimit, func() { defer b.ackWorkers.Done(); b.finishAck(key, ack, false) })
		b.mu.Unlock()
		if err := b.send(room.ID, e.Sender, roomPacket{Version: 1, Kind: "reply", ID: packet.ID, PID: b.ownerText, Conn: packet.Conn, Task: packet.Task, Data: result.Reply}); err != nil {
			b.finishAckLocked(key, ack, false)
		}
	case "ack":
		if room.Owner != b.ownerText {
			return
		}
		key := e.Sender + ":" + packet.ID
		b.mu.Lock()
		ack := b.acks[key]
		b.mu.Unlock()
		if ack != nil && ack.conn == packet.Conn {
			b.finishAckLocked(key, ack, packet.OK)
		}
	case "reply":
		if e.Sender != room.Owner {
			return
		}
		b.mu.Lock()
		ch := b.pending[packet.ID]
		b.mu.Unlock()
		if ch != nil {
			select {
			case ch <- packet:
			default:
			}
		}
	case "push":
		if e.Sender != room.Owner {
			return
		}
		b.mu.Lock()
		peer := b.local
		b.mu.Unlock()
		if peer == nil || packet.Conn != strconv.FormatUint(peer.ConnID(), 10) {
			return
		}
		prepare := b.timing.begin(timingPushPrepare, "", packet.Event)
		data, err := b.rewritePush(packet.Event, packet.Data)
		prepare.endError(err)
		if err != nil {
			return
		}
		write := b.timing.begin(timingPushWrite, "", packet.Event)
		err = peer.SendPush(packet.Event, func(w *demonware.Writer) { w.Raw(data) })
		write.endError(err)
		b.rememberRoom(packet.Event, packet.Data)
	}
}

func (b *P2PBackend) finishAck(key string, ack *roomAck, ok bool) {
	b.mutationMu.Lock()
	defer b.mutationMu.Unlock()
	// This entry point is used only by the existing acknowledgement deadline.
	ack.timing.end(timingTimeout)
	b.finishAckLocked(key, ack, ok)
}

func (b *P2PBackend) finishAckLocked(key string, ack *roomAck, ok bool) {
	b.mu.Lock()
	if b.acks[key] != ack {
		b.mu.Unlock()
		return
	}
	delete(b.acks, key)
	if ack.timer != nil {
		if ack.timer.Stop() {
			b.ackWorkers.Done()
		}
	}
	b.mu.Unlock()
	var err error
	if !ok {
		err = errors.New("room response not delivered")
	}
	ack.timing.endError(err)
	if ack.after != nil {
		ack.after(err)
	}
	b.updateDirectory()
}

func (b *P2PBackend) takeRate(sender string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.rates[sender]
	now := time.Now()
	if now.Sub(r.at) >= time.Second {
		r = roomRate{at: now}
	}
	r.count++
	b.rates[sender] = r
	return r.count <= 120
}

func (b *P2PBackend) remotePeer(pid, conn string) *remoteRoomPeer {
	b.mu.Lock()
	old := b.remotes[pid]
	if old != nil && old.sourceConn == conn {
		b.mu.Unlock()
		return old
	}
	if old != nil {
		previous, _ := strconv.ParseUint(old.sourceConn, 10, 64)
		next, _ := strconv.ParseUint(conn, 10, 64)
		if next < previous {
			b.mu.Unlock()
			return nil
		}
	}
	n, _ := strconv.ParseUint(pid, 10, 64)
	peer := &remoteRoomPeer{backend: b, pid: n, id: (uint64(1) << 63) | b.remoteIDs.Add(1), sourceConn: conn}
	b.remotes[pid] = peer
	b.mu.Unlock()
	if old != nil {
		b.engine.forget(old.ConnID())
		SendNotices(b.engine.matchmaker.Leave(old, 0))
	}
	return peer
}

func (b *P2PBackend) CloseSession(peer lobby.TaskSession) {
	if b.advertisement != nil {
		b.advertisement.CloseSession(peer)
	}
	b.mu.Lock()
	current := b.local
	if current != nil && current.ConnID() == peer.ConnID() {
		b.local = nil
	}
	b.mu.Unlock()
	if current != nil && current.ConnID() == peer.ConnID() {
		b.leaveRoom()
	}
}

func (b *P2PBackend) leaveRoom() {
	b.mutationMu.Lock()
	b.mu.Lock()
	room := b.room
	b.mu.Unlock()
	b.clearRoomLocked()
	b.mutationMu.Unlock()
	if room.ID != "" {
		_ = b.transport.Call(b.ctx, "directory.leave", map[string]any{"room_id": room.ID}, nil)
	}
}

func (b *P2PBackend) clearRoom() {
	b.mutationMu.Lock()
	defer b.mutationMu.Unlock()
	b.clearRoomLocked()
}

func (b *P2PBackend) clearRoomLocked() {
	b.mu.Lock()
	remotes := b.remotes
	acks := b.acks
	b.remotes = map[string]*remoteRoomPeer{}
	b.acks = map[string]*roomAck{}
	b.rates = map[string]roomRate{}
	b.room = directoryRoom{}
	b.filter = ""
	b.activeRoom = 0
	b.generation++
	for _, pending := range b.pending {
		select {
		case pending <- roomPacket{}:
		default:
		}
	}
	b.pending = map[string]chan roomPacket{}
	local := b.local
	b.mu.Unlock()
	for _, ack := range acks {
		if ack.timer != nil {
			if ack.timer.Stop() {
				b.ackWorkers.Done()
			}
		}
		ack.timing.end(timingCanceled)
		if ack.after != nil {
			ack.after(errors.New("room closed"))
		}
	}
	for _, peer := range remotes {
		b.engine.forget(peer.ConnID())
		SendNotices(b.engine.matchmaker.Leave(peer, 0))
	}
	if local != nil {
		SendNotices(b.engine.matchmaker.Leave(local, 0))
	}
}

func (b *P2PBackend) updateDirectory() {
	b.mu.Lock()
	room := b.room
	filter := b.filter
	b.mu.Unlock()
	if room.ID == "" || room.Owner != b.ownerText {
		return
	}
	m := b.engine.matchmaker
	m.mu.Lock()
	capacity := 8
	seats := map[uint64]bool{}
	open := true
	for _, s := range m.waiting {
		capacity = min(capacity, s.Doc.Slots.Max)
		for _, pid := range s.Doc.Members {
			seats[pid] = true
		}
	}
	for _, r := range m.rooms {
		capacity = min(capacity, r.Capacity)
		if allowed, _ := r.HostDoc["lobby_open"].(bool); !allowed {
			open = false
		}
		for _, s := range r.Players {
			for _, pid := range s.Doc.Members {
				seats[pid] = true
			}
		}
	}
	m.mu.Unlock()
	free := max(0, capacity-len(seats))
	if !open {
		free = 0
	}
	_ = b.transport.Call(b.ctx, "directory.update", map[string]any{"room_id": room.ID, "metadata": map[string]any{"playlist": filter, "type": "ctr-async-1", "free_slots": free}}, nil)
}

type localRoomPeer struct {
	lobby.TaskSession
	backend *P2PBackend
}

func (p *localRoomPeer) SendPush(event uint32, write func(*demonware.Writer)) error {
	w := &demonware.Writer{}
	write(w)
	prepare := p.backend.timing.begin(timingPushPrepare, "", event)
	raw, err := p.backend.rewritePush(event, w.Bytes())
	prepare.endError(err)
	if err != nil {
		return err
	}
	span := p.backend.timing.begin(timingPushWrite, "", event)
	err = p.TaskSession.SendPush(event, func(w *demonware.Writer) { w.Raw(raw) })
	span.endError(err)
	if err != nil {
		return err
	}
	p.backend.rememberRoom(event, w.Bytes())
	return nil
}

type remoteRoomPeer struct {
	backend     *P2PBackend
	pid, id     uint64
	sourceConn  string
	lastRequest uint64
}

func (b *P2PBackend) rememberRoom(event uint32, raw []byte) {
	if event != EventCreateLobby && event != EventJoinLobby && event != EventMergeLobby {
		return
	}
	r := demonware.NewReader(raw)
	_, e1 := r.Uint64()
	id, e2 := r.Uint64()
	if event == EventMergeLobby {
		id, e2 = r.Uint64()
	}
	if e1 == nil && e2 == nil {
		b.mu.Lock()
		b.activeRoom = id
		b.mu.Unlock()
	}
}

func (p *remoteRoomPeer) ConnID() uint64 { return p.id }
func (p *remoteRoomPeer) PID() uint64    { return p.pid }
func (p *remoteRoomPeer) Identity() *platform.PlayerID {
	return &platform.PlayerID{PID: p.pid, Kind: "local-room", Title: ps4.CTRTitle}
}
func (p *remoteRoomPeer) Title() uint32                  { return ps4.CTRTitle }
func (p *remoteRoomPeer) Logf(string, ...any)            {}
func (p *remoteRoomPeer) Now() time.Time                 { return time.Now() }
func (p *remoteRoomPeer) Random(raw []byte) (int, error) { return rand.Read(raw) }
func (p *remoteRoomPeer) SendPush(event uint32, write func(*demonware.Writer)) error {
	w := &demonware.Writer{}
	write(w)
	if !roomEvent(event) {
		return errors.New("unsupported room event")
	}
	b := p.backend
	b.mu.Lock()
	room := b.room
	current := b.remotes[strconv.FormatUint(p.pid, 10)]
	b.mu.Unlock()
	if room.ID == "" || current != p {
		return errors.New("remote peer left")
	}
	return b.send(room.ID, strconv.FormatUint(p.pid, 10), roomPacket{Version: 1, Kind: "push", PID: b.ownerText, Conn: p.sourceConn, Event: event, Data: w.Bytes()})
}

func roomEvent(event uint32) bool {
	return event == EventCreateLobby || event == EventJoinLobby || event == EventUpdateLobby || event == EventMergeLobby
}
