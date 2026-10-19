// Package companion owns the bounded, inherited orbiSteamworks NDJSON pipe.
package companion

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/pierluigizagaria/demonware-server/internal/platform/local"
	"github.com/pierluigizagaria/demonware-server/internal/strictjson"
)

const MaxFrame = 98304
const RequestLimit = 30 * time.Second

// These categories describe local control flow, never peer-provided values or
// underlying I/O errors. The first pipe failure is retained through teardown.
type closeReason uint32

const (
	closeOpen closeReason = iota
	closeRequested
	closeContextCanceled
	closeEOF
	closeReadError
	closeWriteError
	closeFrameLimit
	closeMalformedFrame
	closeVersion
	closeOwner
	closeOwnerName
	closeResponse
	closeDuplicateResponse
	closeEvent
	closeEventQueueFull
	closeGameEndpoint
)

func (r closeReason) String() string {
	switch r {
	case closeOpen:
		return "open"
	case closeRequested:
		return "requested"
	case closeContextCanceled:
		return "context-canceled"
	case closeEOF:
		return "eof"
	case closeReadError:
		return "read-error"
	case closeWriteError:
		return "write-error"
	case closeFrameLimit:
		return "frame-limit"
	case closeMalformedFrame:
		return "malformed-frame"
	case closeVersion:
		return "version"
	case closeOwner:
		return "owner"
	case closeOwnerName:
		return "owner-name"
	case closeResponse:
		return "response"
	case closeDuplicateResponse:
		return "duplicate-response"
	case closeEvent:
		return "event"
	case closeEventQueueFull:
		return "event-queue-full"
	case closeGameEndpoint:
		return "game-endpoint"
	default:
		return "unknown"
	}
}

type Event struct {
	Version  int            `json:"v"`
	Event    string         `json:"event"`
	RoomID   string         `json:"room_id"`
	Sender   string         `json:"sender"`
	Owner    string         `json:"owner"`
	Members  []string       `json:"members"`
	Payload  string         `json:"payload"`
	Metadata map[string]any `json:"metadata"`
}

type response struct {
	Version int             `json:"v"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result"`
}

type Pipe struct {
	input             io.ReadCloser
	output            io.WriteCloser
	ctx               context.Context
	cancel            context.CancelFunc
	once              sync.Once
	closedBy          atomic.Uint32
	owner             chan local.Owner
	events            chan Event
	writes            chan []byte
	mu                sync.Mutex
	pending           map[string]chan response
	ids               atomic.Uint64
	workers           sync.WaitGroup
	ownerName         func(string, string) error
	nameUpdatesReady  bool
	virtualEndpoints  bool
	loopbackEndpoints bool
	gameTransport     local.VirtualTransport
	gameEndpoint      func(netip.AddrPort) error
	gameUpdatesReady  bool
	gameEndpointBound bool
}

func New(ctx context.Context, input io.ReadCloser, output io.WriteCloser) *Pipe {
	ctx, cancel := context.WithCancel(ctx)
	p := &Pipe{input: input, output: output, ctx: ctx, cancel: cancel, owner: make(chan local.Owner, 1), events: make(chan Event, 128), writes: make(chan []byte, 128), pending: map[string]chan response{}}
	p.workers.Add(2)
	go func() { defer p.workers.Done(); p.read() }()
	go func() { defer p.workers.Done(); p.write() }()
	return p
}

func (p *Pipe) Done() <-chan struct{} { return p.ctx.Done() }
func (p *Pipe) Events() <-chan Event  { return p.events }
func (p *Pipe) Close() error {
	p.close(closeRequested)
	return nil
}

func (p *Pipe) close(reason closeReason) {
	p.once.Do(func() {
		if p.ctx.Err() != nil {
			reason = closeContextCanceled
		}
		p.closedBy.Store(uint32(reason))
		p.cancel()
		_ = p.input.Close()
		_ = p.output.Close()
	})
}

// CloseReason returns only a fixed diagnostic category. Parent cancellation
// can close Done before either I/O worker observes it.
func (p *Pipe) CloseReason() string {
	reason := closeReason(p.closedBy.Load())
	if reason == closeOpen && p.ctx.Err() != nil {
		reason = closeContextCanceled
	}
	return reason.String()
}

func (p *Pipe) Wait() { p.workers.Wait() }

func (p *Pipe) Owner(ctx context.Context) (local.Owner, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case o := <-p.owner:
		return o, nil
	case <-p.ctx.Done():
		return local.Owner{}, errors.New("companion closed before owner")
	case <-ctx.Done():
		return local.Owner{}, ctx.Err()
	}
}

func (p *Pipe) Ready(ctx context.Context) error {
	p.mu.Lock()
	p.nameUpdatesReady = p.ownerName != nil
	capabilities := []string{}
	if p.nameUpdatesReady {
		capabilities = append(capabilities, "owner.name.v1")
	}
	if p.virtualEndpoints {
		capabilities = append(capabilities, "virtual.endpoints.v1")
	}
	if p.loopbackEndpoints {
		capabilities = append(capabilities, "loopback.endpoints.v1")
	}
	p.gameUpdatesReady = p.gameEndpoint != nil && p.gameTransport.GamePending && !p.gameEndpointBound
	if p.gameUpdatesReady {
		capabilities = append(capabilities, "game.endpoint.v1")
	}
	p.mu.Unlock()
	return p.send(ctx, map[string]any{"v": 1, "event": "ready", "capabilities": capabilities})
}

// EnableVirtualEndpoints acknowledges that NAT and publisher documents have
// been configured for the inherited canonical endpoint. Call before Ready.
func (p *Pipe) EnableVirtualEndpoints() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.virtualEndpoints = true
}

// EnableLoopbackEndpoints acknowledges the loopback-v1 descriptor separately
// from virtual-v1. A child must never negotiate one mode as the other.
func (p *Pipe) EnableLoopbackEndpoints() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loopbackEndpoints = true
}

// EnableGameEndpoint must precede Ready. The private reader applies one exact
// game source before acknowledging it; malformed or repeated updates close the
// pipe. The callback must leave discovery closed if it returns an error.
func (p *Pipe) EnableGameEndpoint(apply func(netip.AddrPort) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gameEndpoint = apply
}

// EnableOwnerNames must precede Ready. It belongs to admission, independently
// of central or peer matchmaking. The reader serializes trusted name updates.
func (p *Pipe) EnableOwnerNames(apply func(string, string) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ownerName = apply
}

func (p *Pipe) Call(ctx context.Context, op string, fields map[string]any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, RequestLimit)
	defer cancel()
	id := strconv.FormatUint(p.ids.Add(1), 10)
	ch := make(chan response, 1)
	p.mu.Lock()
	if p.gameTransport.GamePending && !p.gameEndpointBound && (strings.HasPrefix(op, "directory.") || strings.HasPrefix(op, "peer.") || strings.HasPrefix(op, "advertisement.")) {
		p.mu.Unlock()
		return errors.New("game endpoint is pending")
	}
	if len(p.pending) >= 64 {
		p.mu.Unlock()
		return errors.New("companion request capacity")
	}
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	request := map[string]any{"v": 1, "id": id, "op": op}
	for k, v := range fields {
		if k != "v" && k != "id" && k != "op" {
			request[k] = v
		}
	}
	if err := p.send(ctx, request); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if !r.OK {
			return errors.New("companion operation refused")
		}
		if result != nil && json.Unmarshal(r.Result, result) != nil {
			return errors.New("invalid companion result")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return errors.New("companion closed")
	}
}

func (p *Pipe) send(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw)+1 > MaxFrame {
		return errors.New("invalid companion frame")
	}
	raw = append(raw, '\n')
	select {
	case p.writes <- raw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return errors.New("companion closed")
	}
}

func (p *Pipe) read() {
	reason := closeEOF
	defer func() { p.close(reason) }()
	scanner := bufio.NewScanner(p.input)
	scanner.Buffer(make([]byte, 4096), MaxFrame)
	first := true
	for scanner.Scan() {
		raw := scanner.Bytes()
		var frame map[string]json.RawMessage
		if len(raw)+1 > MaxFrame {
			reason = closeFrameLimit
			return
		}
		if !utf8.Valid(raw) || !strictjson.Decode(string(raw), &frame) || frame == nil {
			reason = closeMalformedFrame
			return
		}
		var version int
		if json.Unmarshal(frame["v"], &version) != nil || version != 1 {
			reason = closeVersion
			return
		}
		if first {
			var event string
			_ = json.Unmarshal(frame["event"], &event)
			if event != "owner" {
				reason = closeOwner
				return
			}
			var owner local.Owner
			if json.Unmarshal(raw, &owner) != nil {
				reason = closeOwner
				return
			}
			if _, err := local.New(owner); err != nil {
				reason = closeOwner
				return
			}
			if owner.Transport != nil {
				p.mu.Lock()
				p.gameTransport = *owner.Transport
				p.mu.Unlock()
			}
			p.owner <- owner
			first = false
			continue
		}
		var eventName string
		_ = json.Unmarshal(frame["event"], &eventName)
		if eventName == "game.endpoint" {
			var id, address string
			var port uint16
			if len(frame) != 5 || json.Unmarshal(frame["id"], &id) != nil || len(id) == 0 || len(id) > 64 || json.Unmarshal(frame["address"], &address) != nil || json.Unmarshal(frame["port"], &port) != nil {
				reason = closeGameEndpoint
				return
			}
			p.mu.Lock()
			apply, enabled, transport := p.gameEndpoint, p.gameUpdatesReady && !p.gameEndpointBound, p.gameTransport
			p.mu.Unlock()
			transport.GamePending, transport.GameAddress, transport.GamePort = false, address, port
			game, _, err := transport.Endpoints()
			if !enabled || apply == nil || err != nil || apply(game) != nil {
				reason = closeGameEndpoint
				return
			}
			p.mu.Lock()
			p.gameEndpointBound, p.gameUpdatesReady = true, false
			p.mu.Unlock()
			if p.send(p.ctx, map[string]any{"v": 1, "event": "game.endpoint.bound", "id": id, "address": address, "port": port}) != nil {
				reason = closeGameEndpoint
				return
			}
			continue
		}
		if eventName == "owner.name" {
			var id, name string
			if len(frame) != 4 || json.Unmarshal(frame["steam_id"], &id) != nil || json.Unmarshal(frame["online_id"], &name) != nil {
				reason = closeOwnerName
				return
			}
			p.mu.Lock()
			apply, enabled := p.ownerName, p.nameUpdatesReady
			p.mu.Unlock()
			if !enabled || apply == nil || apply(id, name) != nil {
				reason = closeOwnerName
				return
			}
			continue
		}
		if value, ok := frame["id"]; ok {
			var id string
			if json.Unmarshal(value, &id) != nil || len(id) == 0 || len(id) > 64 {
				reason = closeResponse
				return
			}
			var r response
			if json.Unmarshal(raw, &r) != nil {
				reason = closeResponse
				return
			}
			p.mu.Lock()
			ch := p.pending[id]
			p.mu.Unlock()
			if ch != nil {
				select {
				case ch <- r:
				default:
					reason = closeDuplicateResponse
					return
				}
			}
			continue
		}
		var e Event
		if json.Unmarshal(raw, &e) != nil {
			reason = closeEvent
			return
		}
		switch e.Event {
		case "peer.message", "room.changed", "room.closed", "room.selected", "error":
		default:
			reason = closeEvent
			return
		}
		p.mu.Lock()
		gamePending := p.gameTransport.GamePending && !p.gameEndpointBound
		p.mu.Unlock()
		if gamePending && e.Event != "error" {
			reason = closeGameEndpoint
			return
		}
		select {
		case p.events <- e:
		default:
			reason = closeEventQueueFull
			return
		}
	}
	if err := scanner.Err(); errors.Is(err, bufio.ErrTooLong) {
		reason = closeFrameLimit
	} else if err != nil {
		reason = closeReadError
	}
}

func (p *Pipe) write() {
	reason := closeContextCanceled
	defer func() { p.close(reason) }()
	for {
		select {
		case raw := <-p.writes:
			if n, err := p.output.Write(raw); err != nil || n != len(raw) {
				reason = closeWriteError
				return
			}
		case <-p.ctx.Done():
			return
		}
	}
}
