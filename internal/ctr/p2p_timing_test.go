package ctr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
)

type timingLog struct {
	mu sync.Mutex
	bytes.Buffer
}

func (l *timingLog) Write(raw []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Buffer.Write(raw)
}
func (l *timingLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Buffer.String()
}
func (l *timingLog) records(t *testing.T) []map[string]string {
	t.Helper()
	var records []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(l.text()), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 10 || fields[0] != "[p2p-timing]" {
			t.Fatal("unexpected diagnostic schema")
		}
		record := map[string]string{}
		for _, field := range fields[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok || record[key] != "" {
				t.Fatal("invalid or repeated diagnostic field")
			}
			record[key] = value
		}
		records = append(records, record)
	}
	return records
}
func (l *timingLog) count(t *testing.T, stage, code, phase, outcome string) int {
	t.Helper()
	n := 0
	for _, r := range l.records(t) {
		if r["stage"] == stage && r["code"] == code && r["phase"] == phase && (outcome == "" || r["outcome"] == outcome) {
			n++
		}
	}
	return n
}
func (l *timingLog) complete(t *testing.T) {
	t.Helper()
	starts := map[string]map[string]string{}
	for _, record := range l.records(t) {
		id := record["seq"]
		if record["phase"] == "start" {
			if starts[id] != nil || record["elapsed_us"] != "0" || record["outcome"] != "pending" {
				t.Fatal("span did not start exactly once")
			}
			starts[id] = record
			continue
		}
		start := starts[id]
		if start == nil || record["phase"] != "end" || record["outcome"] == "pending" {
			t.Fatal("span did not end exactly once")
		}
		for _, key := range []string{"stage", "op", "code"} {
			if start[key] != record[key] {
				t.Fatal("span correlation changed")
			}
		}
		for _, key := range []string{"utc_us", "mono_us", "elapsed_us", "seq", "code"} {
			if value, err := strconv.ParseInt(record[key], 10, 64); err != nil || value < 0 {
				t.Fatal("invalid numeric diagnostic field")
			}
		}
		delete(starts, id)
	}
	if len(starts) != 0 {
		t.Fatal("diagnostic spans remain open after completed work")
	}
}

type timingCallTransport struct {
	RoomTransport
	call func(context.Context, string, map[string]any, any) error
}

func (t *timingCallTransport) Call(ctx context.Context, op string, fields map[string]any, result any) error {
	return t.call(ctx, op, fields, result)
}

func TestP2PTimingCallPrivacyAndErrorIdentity(t *testing.T) {
	var log timingLog
	timing := NewP2PTiming(&log)
	secret := "SENSITIVE-FIXTURE-MARKER"
	for _, op := range []string{"directory.update", secret} {
		for _, want := range []error{nil, errors.New(secret), context.Canceled, context.DeadlineExceeded} {
			called := 0
			inner := &timingCallTransport{call: func(_ context.Context, actual string, fields map[string]any, result any) error {
				called++
				if actual != op || fields["payload"] != secret {
					t.Fatal("timing changed operation input")
				}
				*(result.(*string)) = secret
				return want
			}}
			var result string
			traced := &timedRoomTransport{RoomTransport: inner, timing: timing}
			if got := traced.Call(context.Background(), op, map[string]any{"payload": secret}, &result); got != want || result != secret || called != 1 {
				t.Fatal("timing changed operation result or invocation count")
			}
		}
	}
	timing.begin(timingTask, secret, 7654321).end(timingOK)
	timing.begin(timingPushWrite, secret, 7654321).end(timingError)
	if strings.Contains(log.text(), secret) || strings.Contains(log.text(), "7654321") {
		t.Fatal("diagnostics included non-allowlisted input")
	}
	for _, outcome := range []string{"ok", "error", "canceled", "timeout"} {
		if log.count(t, "call", "0", "end", outcome) != 2 {
			t.Fatal("operation outcome was not classified")
		}
	}
	log.complete(t)
}

type failingTimingPeer struct {
	*roomFixturePeer
	err error
}

func (p *failingTimingPeer) SendPush(uint32, func(*demonware.Writer)) error { return p.err }

func TestP2PTimingPreservesReplyAckAndPushBehavior(t *testing.T) {
	var baseline [][]byte
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			var ownerLog, memberLog timingLog
			var ownerTiming, memberTiming *P2PTiming
			if enabled {
				file, err := os.Create(filepath.Join(t.TempDir(), "timing.log"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = file.Close() })
				ownerTiming = NewP2PTiming(io.MultiWriter(&ownerLog, file))
				memberTiming = NewP2PTiming(io.MultiWriter(&memberLog, file))
			}
			hub := newFixtureHub()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			decorate := func(inner RoomTransport) RoomTransport {
				return &timingCallTransport{RoomTransport: inner, call: func(ctx context.Context, op string, fields map[string]any, out any) error {
					if op == "peer.send" {
						raw, _ := base64.StdEncoding.DecodeString(fields["payload"].(string))
						var packet roomPacket
						_ = json.Unmarshal(raw, &packet)
						if packet.Kind == "reply" && packet.Task == TaskStartSearch {
							close(entered)
							select {
							case <-release:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
					}
					return inner.Call(ctx, op, fields, out)
				}}
			}
			a, pa := newTimedRoomFixture(t, hub, 101, ownerTiming, decorate)
			b, pb := newTimedRoomFixture(t, hub, 102, memberTiming)
			start := time.Now()
			var observed [][]byte
			for _, pair := range []struct {
				b *P2PBackend
				p *roomFixturePeer
			}{{a, pa}, {b, pb}} {
				result := pair.b.Handle(pair.p, taskRequest(TaskSetPlayerInfo, stringArgs(parityRoomInfo(pair.p.PID()))))
				if result.AfterReply != nil {
					t.Fatal("timing added a callback where none existed")
				}
				observed = append(observed, result.Reply)
				deliverResult(t, result)
			}
			first := a.Handle(pa, taskRequest(TaskStartSearch, searchArgs(pa.PID())))
			observed = append(observed, first.Reply)
			deliverResult(t, first)
			returned := make(chan lobby.TaskResult, 1)
			go func() { returned <- b.Handle(pb, taskRequest(TaskStartSearch, searchArgs(pb.PID()))) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("remote response did not reach controlled gate")
			}
			select {
			case <-returned:
				t.Fatal("task returned before remote response")
			default:
			}
			if enabled && (memberLog.count(t, "remote", "18", "start", "") != 1 || memberLog.count(t, "remote", "18", "end", "") != 0) {
				t.Fatal("delayed remote response was not attributed to its roundtrip")
			}
			once.Do(func() { close(release) })
			var second lobby.TaskResult
			select {
			case second = <-returned:
			case <-time.After(3 * time.Second):
				t.Fatal("remote task did not return")
			}
			observed = append(observed, second.Reply)
			settleRoomEvents(t, hub)
			select {
			case <-pa.pushes:
				t.Fatal("search activated before game reply delivery")
			default:
			}
			if enabled && (ownerLog.count(t, "ack", "18", "start", "") != 1 || ownerLog.count(t, "ack", "18", "end", "") != 0 || memberLog.count(t, "reply", "18", "end", "") != 0) {
				t.Fatal("ACK/reply diagnostic ended before the game reply callback")
			}
			deliverResult(t, second)
			created := awaitRoomPush(t, pa)
			observed = append(observed, created.data)
			ids, host, _ := pushDocuments(t, created)
			w := &demonware.Writer{}
			w.Uint64(ids[1])
			w.Uint64(ids[2])
			w.String(host)
			sync := a.Handle(pa, taskRequest(TaskSyncLobbyDocuments, w.Bytes()))
			observed = append(observed, sync.Reply)
			deliverResult(t, sync)
			joined := awaitRoomPush(t, pb)
			observed = append(observed, joined.data)
			settleRoomEvents(t, hub)
			assertRoomRequestsSettled(t, a, b)
			if created.event != EventCreateLobby || joined.event != EventJoinLobby {
				t.Fatal("event ordering changed")
			}
			failure := errors.New("SENSITIVE-PUSH-ERROR")
			bad := &localRoomPeer{TaskSession: &failingTimingPeer{roomFixturePeer: pa, err: failure}, backend: a}
			if bad.SendPush(created.event, func(w *demonware.Writer) { w.Raw(created.data) }) != failure {
				t.Fatal("push error identity changed")
			}
			t.Logf("same controlled room fixture: tracing=%t elapsed_us=%d (measurement only)", enabled, time.Since(start).Microseconds())
			if !enabled {
				baseline = observed
				if ownerLog.text() != "" || memberLog.text() != "" {
					t.Fatal("disabled timing emitted output")
				}
			} else {
				if !reflect.DeepEqual(observed, baseline) {
					t.Fatal("tracing changed task or push bytes")
				}
				if ownerLog.count(t, "ack", "18", "end", "ok") != 1 || ownerLog.count(t, "push_write", "2005", "end", "error") != 1 || memberLog.count(t, "push_write", "2001", "end", "ok") != 1 {
					t.Fatal("missing delivery or ACK outcome")
				}
				if strings.Contains(ownerLog.text(), "SENSITIVE-PUSH-ERROR") {
					t.Fatal("push error text leaked")
				}
				ownerLog.complete(t)
				memberLog.complete(t)
			}
		})
	}
}

func TestP2PTimingFailedReplyPreservesRollback(t *testing.T) {
	var log timingLog
	timing := NewP2PTiming(&log)
	hub := newFixtureHub()
	a, pa := newTimedRoomFixture(t, hub, 101, timing)
	b, pb := newTimedRoomFixture(t, hub, 102, timing)
	for _, pair := range []struct {
		b *P2PBackend
		p *roomFixturePeer
	}{{a, pa}, {b, pb}} {
		deliverResult(t, pair.b.Handle(pair.p, taskRequest(TaskSetPlayerInfo, stringArgs(parityRoomInfo(pair.p.PID())))))
	}
	deliverResult(t, a.Handle(pa, taskRequest(TaskStartSearch, searchArgs(pa.PID()))))
	result := b.Handle(pb, taskRequest(TaskStartSearch, searchArgs(pb.PID())))
	result.AfterReply(errors.New("SENSITIVE-DELIVERY-ERROR"))
	settleRoomEvents(t, hub)
	assertRoomRequestsSettled(t, a, b)
	select {
	case <-pa.pushes:
		t.Fatal("failed reply still activated room")
	default:
	}
	if log.count(t, "ack", "18", "end", "error") != 1 || log.count(t, "reply", "18", "end", "error") != 1 || strings.Contains(log.text(), "SENSITIVE-DELIVERY-ERROR") {
		t.Fatal("failed reply was not reported safely")
	}
	log.complete(t)
}
