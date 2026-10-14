// Lobby remote tasks (bdRemoteTask), read from the NSO.
//
// Client request (encrypted message, inner type 0x86): a TYPED bdByteBuffer
// whose first byte — the service identifier — is written raw:
//
//	u8 service | 03 u8 task | typed arguments
//
// Reply (inner type 0x01, pump 0xBE96E0 -> 0xBE3FE0 -> 0xC00370):
//
//	0A u64 transaction | 08 u32 error | 03 u8 | 08 u32 numResults | 08 u32 total | results
//
// Error 0 yields results, 200 leaves the task pending, any other value makes
// it fail cleanly. Tasks are served in order (the first pending one takes the
// reply): there is no identifier to match.
//
// Type tags (readers 0xBD90E0/0xBD9170/0xBDA100/0xBDA220/0xBDA4C0):
// 01 bool, 03 u8, 08 u32, 0A u64, 10 NUL-terminated string, 13 blob (08 u32 + bytes).

package lobby

import (
	"sync/atomic"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

const (
	svcStorage           = 10
	taskGetPublisherFile = 21
)

// Error returned for an unhandled task: nonzero and different from 200, so
// the task fails instead of timing out (a timeout drops the lobby).
const ErrUnhandled = 1

type ReplyCounter struct{ Transactions atomic.Uint64 }

// sendPush sends a pushed message (inner type 0x02). bdLobbyService::handlePushMessage reads a
// tagged u32: the bdEventType, resolved in the table of registered handlers.
func (l *Session) sendPush(event uint32, build func(w *demonware.Writer)) error {
	w := &demonware.Writer{}
	w.Uint32(event)
	if build != nil {
		build(w)
	}
	return l.sendEncrypted(demonware.InnerPush, w.Bytes())
}

// ProcessTask handles a decrypted request. payload = bytes after the inner type.
func (l *Session) ProcessTask(payload []byte) {
	// This check linearizes task admission with RequestStop. Once admitted,
	// the session worker owns the whole reply and AfterReply operation.
	if l.isStopping() {
		return
	}
	// Inherited router compatibility, characterized by the raw-bandwidth fixture.
	// Its general protocol meaning is not established. Preserve this ordering.
	if len(payload) == 18 && payload[0] == 18 && payload[1] == 1 && payload[2] == 0 && payload[3] == 1 {
		_ = l.sendEncrypted(demonware.InnerTaskReply, l.TaskReply(1, ErrUnhandled, nil))
		return
	}
	if len(payload) < 3 {
		l.logf("task: payload too short %X", payload)
		return
	}
	service := payload[0]
	r := demonware.NewReader(payload[1:])
	task, err := r.Uint8()
	if err != nil {
		// Never leave a task unanswered: the game waits for it until the timeout, which
		// fails the whole LSG login sequence and restarts a full reconnection.
		if len(payload) >= 3 {
			task = payload[2]
		}
		if l.Table.rejectsUnreadableTask(service) {
			_ = l.sendEncrypted(demonware.InnerTaskReply, l.TaskReply(task, ErrUnhandled, nil))
			return
		}
		l.logf("task: service %d task id unreadable (%v), empty success task=%d: %X",
			service, err, task, payload)
		if err := l.sendEncrypted(demonware.InnerTaskReply, l.TaskReply(task, 0, nil)); err != nil {
			l.logf("send reply: %v", err)
		}
		return
	}
	l.Telemetry.NoteTask(l.N, l.Presence.ConnPID(l.N), service, task)

	l.DeliverResult(l.Table.Handle(l, TaskRequest{Service: service, Task: task, Args: r, Payload: payload}))
}

func (l *Session) DeliverResult(result TaskResult) {
	var sendErr error
	if result.AfterReply != nil {
		defer func() {
			l.AfterReplyActive = true
			defer func() { l.AfterReplyActive = false }()
			result.AfterReply(sendErr)
		}()
	}
	sendErr = l.sendEncrypted(demonware.InnerTaskReply, result.Reply)
	l.ReplyErr = sendErr
	if sendErr != nil {
		l.logf("send reply: %v", sendErr)
	}
}

// TaskResult is owned by one request; the router invokes AfterReply exactly
// once, passing the error from that request's send attempt.
type TaskResult struct {
	Reply      []byte
	AfterReply func(sendErr error)
}

func (replies *ReplyCounter) TaskReply(task byte, code uint32, results func(w *demonware.Writer) uint32) []byte {
	return demonware.TaskReply(replies.Transactions.Add(1), task, code, results)
}

func (replies *ReplyCounter) HTTPProxyReply(task byte, status uint32, body []byte) []byte {
	return demonware.HTTPProxyReply(replies.Transactions.Add(1), task, status, body)
}

func (replies *ReplyCounter) StructTaskReply(task byte, body []byte) []byte {
	return demonware.StructReply(replies.Transactions.Add(1), task, body)
}

func (replies *ReplyCounter) NextTransaction() uint64 { return replies.Transactions.Add(1) }
