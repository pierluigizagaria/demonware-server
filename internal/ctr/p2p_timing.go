package ctr

import (
	"context"
	"errors"
	"io"
	"log"
	"sync/atomic"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/lobby"
)

// P2PTiming is an optional local diagnostic sink, never part of the peer wire.
// Its input surface contains no identities, request fields, results or error text.
type P2PTiming struct {
	log   *log.Logger
	start time.Time
	ids   atomic.Uint64
}

func NewP2PTiming(output io.Writer) *P2PTiming {
	if output == nil {
		return nil
	}
	return &P2PTiming{log: log.New(output, "", 0), start: time.Now()}
}

type timingStage uint8

const (
	timingCall timingStage = iota
	timingTask
	timingReply
	timingAfterReply
	timingRemote
	timingAck
	timingPushPrepare
	timingPushWrite
)

var timingStages = [...]string{"call", "task", "reply", "after_reply", "remote", "ack", "push_prepare", "push_write"}

type timingOutcome uint8

const (
	timingPending timingOutcome = iota
	timingOK
	timingError
	timingCanceled
	timingTimeout
	timingRefused
)

var timingOutcomes = [...]string{"pending", "ok", "error", "canceled", "timeout", "refused"}

type timingSpan struct {
	sink     *P2PTiming
	start    time.Time
	seq      uint64
	stage    timingStage
	op       string
	code     uint32
	finished atomic.Bool
}

func timingOperation(op string) string {
	switch op {
	case "directory.search", "directory.join", "directory.create", "directory.members", "directory.update", "directory.leave", "peer.endpoint", "peer.send", "advertisement.get", "advertisement.set", "name.lookup", "name.resolve":
		return op
	}
	return "none"
}

func timingCode(stage timingStage, code uint32) uint32 {
	if stage == timingCall {
		return 0
	}
	if stage == timingPushPrepare || stage == timingPushWrite {
		if roomEvent(code) {
			return code
		}
		return 0
	}
	switch code {
	case TaskSetPlayerInfo, TaskGetPlayerToken, TaskQoSHostsReply, TaskGetMMStatus, TaskInitMatchMaking, TaskStartMatchMaking, 8, TaskLobbyDisbanded, TaskGetLobbyDocuments, TaskAckExpectGame, TaskSyncLobbyDocuments, TaskInitiateDCQoS, TaskStartSearch:
		return code
	}
	return 0
}

func (t *P2PTiming) begin(stage timingStage, op string, code uint32) *timingSpan {
	if t == nil || int(stage) >= len(timingStages) {
		return nil
	}
	s := &timingSpan{sink: t, start: time.Now(), seq: t.ids.Add(1), stage: stage, op: timingOperation(op), code: timingCode(stage, code)}
	s.record(s.start, "start", timingPending)
	return s
}

func (s *timingSpan) record(now time.Time, phase string, outcome timingOutcome) {
	s.sink.log.Printf("[p2p-timing] utc_us=%d mono_us=%d seq=%d stage=%s op=%s code=%d phase=%s elapsed_us=%d outcome=%s",
		now.UnixMicro(), now.Sub(s.sink.start).Microseconds(), s.seq, timingStages[s.stage], s.op, s.code, phase, now.Sub(s.start).Microseconds(), timingOutcomes[outcome])
}

func (s *timingSpan) end(outcome timingOutcome) {
	if s == nil || s.finished.Swap(true) {
		return
	}
	if outcome == timingPending || int(outcome) >= len(timingOutcomes) {
		outcome = timingError
	}
	s.record(time.Now(), "end", outcome)
}

func (s *timingSpan) endError(err error) {
	if s == nil {
		return
	}
	outcome := timingOK
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		outcome = timingTimeout
	case errors.Is(err, context.Canceled):
		outcome = timingCanceled
	case err != nil:
		outcome = timingError
	}
	s.end(outcome)
}

type timedRoomTransport struct {
	RoomTransport
	timing *P2PTiming
}

func (t *timedRoomTransport) Call(ctx context.Context, op string, fields map[string]any, result any) (err error) {
	span := t.timing.begin(timingCall, op, 0)
	defer func() { span.endError(err) }()
	return t.RoomTransport.Call(ctx, op, fields, result)
}

func (b *P2PBackend) Handle(peer lobby.TaskSession, request lobby.TaskRequest) lobby.TaskResult {
	if b.timing == nil {
		return b.handle(peer, request)
	}
	span := b.timing.begin(timingTask, "", uint32(request.Task))
	result := b.handle(peer, request)
	outcome := timingOK
	if !successfulReply(result.Reply) {
		outcome = timingRefused
	}
	span.end(outcome)
	if after := result.AfterReply; after != nil {
		reply := b.timing.begin(timingReply, "", uint32(request.Task))
		result.AfterReply = func(err error) {
			reply.endError(err)
			span := b.timing.begin(timingAfterReply, "", uint32(request.Task))
			defer span.endError(err)
			after(err)
		}
	}
	return result
}
