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
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
func (l *lobbyConn) onTask(payload []byte) {
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
	noteTask(l, service, task)

	var reply []byte
	switch {
	case service == 38 && task == 6:
		reply = l.onPlatformIdentityReport(task, r)
	case service == 4 && l.player != nil && isCTRTitle(l.player.Title):
		reply = l.onCTRStats(task, r)
	case service == svcTitleUtilities && task == taskGetServerTime:
		now := uint32(l.now().Unix())
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Uint32(now); return 1 })
		l.logf("task bdTitleUtilities.getServerTime -> %d", now)

	case service == svcStorage && task == taskGetPublisherFile:
		ctx, err1 := r.String()
		name, err2 := r.String()
		if err1 != nil || err2 != nil {
			l.logf("getPublisherFile: unreadable arguments: %X", payload)
			reply = l.taskReply(task, errUnhandled, nil)
			break
		}
		data, path := l.publisherFile(name)
		if data == nil {
			// No result rather than an error: the game treats a "missing
			// file" as a normal case.
			reply = l.taskReply(task, 0, nil)
			l.logf("task bdStorage.getPublisherFile ctx=%q file=%q -> MISSING", ctx, name)
			break
		}
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Blob(data); return 1 })
		l.logf("task bdStorage.getPublisherFile ctx=%q file=%q -> %s (%d bytes)", ctx, name, path, len(data))

	case service == svcTitleUtilities && task == 9:
		reply = l.onGetUserNames(task, r)

	case service == svcTitleUtilities && task == 10:
		reply = l.onGetUserIDs(task, r)

	case service == 29 && (task == 1 || task == 4):
		if reply = l.onUserData(task, r); reply == nil {
			reply = l.taskReply(task, 0, nil)
		}

	case service == 138:
		ctx, err := r.String()
		if err != nil || ctx == "" {
			reply = l.taskReply(task, errUnhandled, nil)
			break
		}
		if task == mmCreateSession || task == mmUpdateSession || task == mmDeleteSession || task == mmUpdatePlayers || task == mmRequestSessionID || task == mmInitializeSession {
			reply = l.onMatchMakingContext(task, r, "ctr:"+ctx)
		} else if task == 14 {
			reply = l.onFriendSessions(task, r, "ctr:"+ctx)
		} else {
			l.logf("CTR matchmaking unimplemented task=%d context=%q", task, ctx)
			reply = l.taskReply(task, errUnhandled, nil)
		}
	case service == svcRichPresence:
		reply = l.onRichPresence(task, r)

	case service == svcAsyncMatchMaking && task == taskSetPlayerInfo:
		// bdAsyncMatchMaking::setPlayerInfo(const char*, u32): the game uploads its
		// listen_server{local_address,security_id,security_key} here. The function has no
		// result, so an empty success is the right answer. It is the FIRST task of the
		// matchmaking registration: failing it brings down everything after it.
		if doc, err := r.String(); err == nil && doc != "" {
			l.lobbyDoc = doc
		}
		reply = l.taskReply(task, 0, nil)
		l.logf("task bdAsyncMatchMaking.setPlayerInfo -> success (%d bytes)", len(l.lobbyDoc))

	case service == svcAsyncMatchMaking && task == taskSyncLobbyDocuments:
		id, e1 := r.Uint64()
		version, e2 := r.Uint64()
		raw, e3 := r.String()
		ok := false
		if e1 == nil && e2 == nil && e3 == nil {
			var notices []ctrNotice
			ok, notices = ctrMM.syncDoc(l, id, version, raw)
			l.afterReply = func() { sendCTRNotices(notices) }
		}
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Bool(ok); return 1 })
		l.logf("CTR sync lobby=%d version=%d accepted=%v", id, version, ok)

	case service == svcAsyncMatchMaking && task == taskGetPlayerToken:
		// getMatchMakingPlayerToken -> bdUInt64Result (readUInt64). This is the
		// requestPlayerToken step of CNetworkTaskAsyncMatchMakingRegistration: without a token the
		// registration state machine cannot advance.
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Uint64(l.pid()); return 1 })
		l.logf("task bdAsyncMatchMaking.getMatchMakingPlayerToken -> %d", l.pid())

	case service == svcAsyncMatchMaking && task == taskStartSearch:
		raw, err := r.String()
		if err != nil {
			reply = l.taskReply(task, errUnhandled, nil)
			break
		}
		search, err := parseCTRSearch(l, raw)
		if err == nil {
			err = ctrMM.reserve(search)
		}
		if err != nil {
			l.logf("CTR search rejected: %v", err)
			reply = l.taskReply(task, errUnhandled, nil)
			break
		}
		doc := "{\"mm_id\":" + strconv.FormatUint(search.id, 10) + "}"
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.String(doc); return 1 })
		l.afterReply = func() {
			if l.replyErr != nil {
				ctrMM.release(search) // the client never learned of this search
				return
			}
			notices := ctrMM.activate(search)
			l.logf("CTR search id=%d registered; seats=%d filter=%s notifications=%d",
				search.id, search.seats, search.filterID, len(notices))
			sendCTRNotices(notices)
		}

	case service == svcAsyncMatchMaking && task == 8:
		id, err := r.Uint64()
		ok := err == nil && ctrMM.cancel(l, id)
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Bool(ok); return 1 })
	case service == svcAsyncMatchMaking && task == taskLobbyDisbanded:
		id, err := r.Uint64()
		if err == nil {
			notices := ctrMM.leave(l, id)
			l.afterReply = func() { sendCTRNotices(notices) }
		}
		reply = l.taskReply(task, 0, nil)

	case service == svcAsyncMatchMaking &&
		(task == taskInitMatchMaking || task == taskStartMatchMaking ||
			task == taskGetMMStatus || task == taskInitiateDCQoS):
		// All of these return a bdStringResult (readString).
		id := strconv.FormatUint(l.pid(), 10)
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.String(id); return 1 })
		l.logf("task bdAsyncMatchMaking task=%d -> string %q", task, id)

	case service == svcAsyncMatchMaking &&
		(task == taskQoSHostsReply || task == taskAckExpectGame):
		// No result.
		reply = l.taskReply(task, 0, nil)
		l.logf("task bdAsyncMatchMaking task=%d -> empty success", task)

	case service == svcAsyncMatchMaking && task == taskGetLobbyDocuments:
		id, err := r.Uint64()
		h, b, ok := ctrMM.documents(l, id)
		if err != nil || !ok {
			reply = l.taskReply(task, errUnhandled, nil)
			break
		}
		reply = l.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.String(h); w.String(b); return 1 })

	case service == svcHTTPProxy && task == 1:
		// bdABTesting::enroll. The body is parsed as JSON: expiresIn, ABToken,
		// enrollments. No experiment is active, but the reply must be well
		// formed, or the task fails on every connection.
		// ABToken NON-EMPTY: bdABTestingEnrollResponse reads it with getString, and an empty string
		// fails deserialization -> bdRemoteTask error 4 -> the login sequence fails.
		body := []byte(`{"expiresIn":3600,"ABToken":"nextendo","enrollments":[]}`)
		reply = l.httpProxyReply(task, 200, body)
		l.logf("task bdABTesting.enroll -> 200 (%d bytes)", len(body))

	case service == svcMarketingComms:
		reply = l.onMarketingComms(task, r)

	case service == svcAchievements:
		reply = l.onAchievements(task, payload)

	case service == svcMarketplace:
		if reply = l.onMarketplace(task, payload); reply == nil {
			reply = l.taskReply(task, errUnhandled, nil)
			l.logf("task marketplace UNHANDLED task=%d args:\n%s", task, hex.Dump(payload))
		}

	case service == svcMatchMaking:
		if reply = l.onMatchMaking(task, r); reply == nil {
			reply = l.taskReply(task, 0, nil)
			l.logf("task matchmaking UNHANDLED task=%d args:\n%s", task, hex.Dump(payload))
		}

	default:
		// Success with no result: harmless for list requests, whereas an
		// error may mark the service unavailable on the game side.
		reply = l.taskReply(task, 0, nil)
		l.logf("task UNHANDLED service=%d task=%d (empty success) args:\n%s", service, task, hex.Dump(payload))
	}

	if pending := l.afterReply; pending != nil {
		l.afterReply = nil
		defer pending()
	}
	l.replyErr = l.sendEncrypted(demonware.InnerTaskReply, reply)
	if l.replyErr != nil {
		l.logf("send reply: %v", l.replyErr)
	}
}

// publisherFile looks the file up in the pubfiles folder, ignoring case (the
// game asks for "Config.txt", the generator writes "config.txt").
func (l *lobbyConn) publisherFile(name string) ([]byte, string) {
	if data, path, ok := riftFile(name, l.now()); ok {
		return data, path
	}
	if data, path, ok := generatedPubfile(name, l.now()); ok {
		return data, path
	}
	base := filepath.Base(name)
	entries, err := os.ReadDir(l.pubDir)
	if err != nil {
		return nil, ""
	}
	candidates := []string{base}
	if alias, ok := pubFileAliases[strings.ToLower(base)]; ok {
		candidates = append(candidates, alias)
	}
	for _, want := range candidates {
		for _, e := range entries {
			if strings.EqualFold(e.Name(), want) {
				p := filepath.Join(l.pubDir, e.Name())
				if b, err := os.ReadFile(p); err == nil {
					return b, p
				}
			}
		}
	}
	return nil, ""
}

// Real names requested by the game -> names written by the generator (taken
// from d3hack's cache). Seen live: Config.txt, Seasons.txt, Blacklist.txt.
var pubFileAliases = map[string]string{
	"seasons.txt":   "seasons_config.txt",
	"blacklist.txt": "blacklist_config.txt",
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
