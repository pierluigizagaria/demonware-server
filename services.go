package main

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

import (
	"sync/atomic"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

const (
	svcStorage          = 10
	svcTitleUtilities   = 12
	svcAsyncMatchMaking = 145 // bdAsyncMatchMaking
	svcHTTPProxy        = 194 // bdABTesting, via bdHTTPProxyRequest/Response

	// Numbers AND result types read from the bdTaskParams of each bdAsyncMatchMaking
	// function. bdUInt64Result::deserialize = readUInt64, bdStringResult = readString,
	// bdBoolResult = readBool, bdLobbyDocuments = two strings.
	taskSetPlayerInfo      = 2  // no result
	taskGetPlayerToken     = 3  // bdUInt64Result
	taskQoSHostsReply      = 4  // no result
	taskGetMMStatus        = 5  // bdStringResult
	taskInitMatchMaking    = 6  // bdStringResult
	taskStartMatchMaking   = 7  // bdStringResult
	taskLobbyDisbanded     = 10 // no result
	taskGetLobbyDocuments  = 13 // bdLobbyDocuments
	taskAckExpectGame      = 14 // no result
	taskSyncLobbyDocuments = 15 // bdBoolResult
	taskInitiateDCQoS      = 17 // bdStringResult
	taskStartSearch        = 18 // bdStringResult

	taskGetPublisherFile = 21
	taskGetServerTime    = 6
)

// Error returned for an unhandled task: nonzero and different from 200, so
// the task fails instead of timing out (a timeout drops the lobby).
const errUnhandled = 1

type replyCounter struct{ transactions atomic.Uint64 }

// sendPush sends a pushed message (inner type 0x02). bdLobbyService::handlePushMessage reads a
// tagged u32: the bdEventType, resolved in the table of registered handlers.
func (l *lobbyConn) sendPush(event uint32, build func(w *demonware.Writer)) error {
	w := &demonware.Writer{}
	w.Uint32(event)
	if build != nil {
		build(w)
	}
	return l.sendEncrypted(demonware.InnerPush, w.Bytes())
}

// onTask handles a decrypted request. payload = bytes after the inner type.
func (l *lobbyConn) processTask(payload []byte) {
	if len(payload) == 18 && payload[0] == 18 && payload[1] == 1 && payload[2] == 0 && payload[3] == 1 {
		_ = l.sendEncrypted(demonware.InnerTaskReply, l.taskReply(1, errUnhandled, nil))
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
		if service == svcMarketplace || service == svcMarketingComms {
			_ = l.sendEncrypted(demonware.InnerTaskReply, l.taskReply(task, errUnhandled, nil))
			return
		}
		l.logf("task: service %d task id unreadable (%v), empty success task=%d: %X",
			service, err, task, payload)
		if err := l.sendEncrypted(demonware.InnerTaskReply, l.taskReply(task, 0, nil)); err != nil {
			l.logf("send reply: %v", err)
		}
		return
	}
	l.telemetry.noteTask(l, service, task)

	l.deliverResult(l.table.Handle(l, taskRequest{Service: service, Task: task, Args: r, Payload: payload}))
}

func (l *lobbyConn) deliverResult(result taskResult) {
	var sendErr error
	if result.AfterReply != nil {
		defer func() {
			l.afterReplyActive = true
			defer func() { l.afterReplyActive = false }()
			result.AfterReply(sendErr)
		}()
	}
	sendErr = l.sendEncrypted(demonware.InnerTaskReply, result.Reply)
	l.replyErr = sendErr
	if sendErr != nil {
		l.logf("send reply: %v", sendErr)
	}
}

// taskResult is owned by one request; the router invokes AfterReply exactly
// once, passing the error from that request's send attempt.
type taskResult struct {
	Reply      []byte
	AfterReply func(sendErr error)
}

func (replies *replyCounter) taskReply(task byte, code uint32, results func(w *demonware.Writer) uint32) []byte {
	return demonware.TaskReply(replies.transactions.Add(1), task, code, results)
}
func (replies *replyCounter) httpProxyReply(task byte, status uint32, body []byte) []byte {
	return demonware.HTTPProxyReply(replies.transactions.Add(1), task, status, body)
}
func (replies *replyCounter) structTaskReply(task byte, body []byte) []byte {
	return demonware.StructReply(replies.transactions.Add(1), task, body)
}
