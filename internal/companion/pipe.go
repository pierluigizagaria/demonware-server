// Package companion owns the bounded, inherited orbiSteamworks NDJSON pipe.
package companion

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/pierluigizagaria/demonware-server/internal/platform/local"
	"github.com/pierluigizagaria/demonware-server/internal/strictjson"
)

const MaxFrame = 98304
const RequestLimit = 30 * time.Second

type Event struct {
	Version int      `json:"v"`
	Event   string   `json:"event"`
	RoomID  string   `json:"room_id"`
	Sender  string   `json:"sender"`
	Owner   string   `json:"owner"`
	Members []string `json:"members"`
	Payload string   `json:"payload"`
}

type response struct {
	Version int             `json:"v"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result"`
}

type Pipe struct {
	input   io.ReadCloser
	output  io.WriteCloser
	ctx     context.Context
	cancel  context.CancelFunc
	once    sync.Once
	owner   chan local.Owner
	events  chan Event
	writes  chan []byte
	mu      sync.Mutex
	pending map[string]chan response
	ids     atomic.Uint64
	workers sync.WaitGroup
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
	p.once.Do(func() { p.cancel(); _ = p.input.Close(); _ = p.output.Close() })
	return nil
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
	return p.send(ctx, map[string]any{"v": 1, "event": "ready"})
}

func (p *Pipe) Call(ctx context.Context, op string, fields map[string]any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, RequestLimit)
	defer cancel()
	id := strconv.FormatUint(p.ids.Add(1), 10)
	ch := make(chan response, 1)
	p.mu.Lock()
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
	defer p.Close()
	scanner := bufio.NewScanner(p.input)
	scanner.Buffer(make([]byte, 4096), MaxFrame)
	first := true
	for scanner.Scan() {
		raw := scanner.Bytes()
		var frame map[string]json.RawMessage
		if len(raw)+1 > MaxFrame || !utf8.Valid(raw) || !strictjson.Decode(string(raw), &frame) || frame == nil {
			return
		}
		var version int
		if json.Unmarshal(frame["v"], &version) != nil || version != 1 {
			return
		}
		if first {
			var event string
			_ = json.Unmarshal(frame["event"], &event)
			if event != "owner" {
				return
			}
			var owner local.Owner
			if json.Unmarshal(raw, &owner) != nil {
				return
			}
			if _, err := local.New(owner); err != nil {
				return
			}
			p.owner <- owner
			first = false
			continue
		}
		if value, ok := frame["id"]; ok {
			var id string
			if json.Unmarshal(value, &id) != nil || len(id) == 0 || len(id) > 64 {
				return
			}
			var r response
			if json.Unmarshal(raw, &r) != nil {
				return
			}
			p.mu.Lock()
			ch := p.pending[id]
			p.mu.Unlock()
			if ch != nil {
				select {
				case ch <- r:
				default:
					return
				}
			}
			continue
		}
		var e Event
		if json.Unmarshal(raw, &e) != nil {
			return
		}
		switch e.Event {
		case "peer.message", "room.changed", "room.closed", "error":
		default:
			return
		}
		select {
		case p.events <- e:
		default:
			return
		}
	}
}

func (p *Pipe) write() {
	defer p.Close()
	for {
		select {
		case raw := <-p.writes:
			if _, err := p.output.Write(raw); err != nil {
				return
			}
		case <-p.ctx.Done():
			return
		}
	}
}
