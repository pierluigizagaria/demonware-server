package main

import (
	"encoding/hex"
	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	"strconv"
	"sync"
)

type utilityHandler struct{ *presenceHandler }
type userDataHandler struct{ *presenceHandler }
type richPresenceHandler struct{ *presenceHandler }
type httpProxyHandler struct{ *replyCounter }
type storageHandler struct {
	*replyCounter
	files  *publisherFiles
	pubDir string
}
type contextHandler struct {
	*replyCounter
	registry *sessionRegistry
	presence *presenceStore
	relay    *relayTable
}
type diabloMatchHandler struct{ *contextHandler }
type asyncHandler struct {
	*replyCounter
	matchmaker *ctrMatchmaker
	mu         sync.Mutex
	info       map[uint64]string
}

func (h *asyncHandler) playerInfo(id uint64) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.info[id]
}
func (h *asyncHandler) setPlayerInfo(id uint64, info string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.info[id] = info
}
func (h *asyncHandler) forget(id uint64) { h.mu.Lock(); defer h.mu.Unlock(); delete(h.info, id) }

func defaultTask(replies *replyCounter, peer taskSession, request taskRequest) taskResult {
	reply := replies.taskReply(request.Task, 0, nil)
	peer.Logf("task UNHANDLED service=%d task=%d (empty success) args:\n%s", request.Service, request.Task, hex.Dump(request.Payload))
	return taskResult{Reply: reply}
}

func (handler *platformHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 38 && task == 6:
		reply = handler.onPlatformIdentityReport(peer, task, r)

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *statsHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 4 && peer.Identity() != nil && isCTRTitle(peer.Identity().Title):
		reply = handler.onCTRStats(peer, task, r)

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *utilityHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == svcTitleUtilities && task == taskGetServerTime:
		now := uint32(peer.Now().Unix())
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Uint32(now); return 1 })
		peer.Logf("task bdTitleUtilities.getServerTime -> %d", now)

	case service == svcTitleUtilities && task == 9:
		reply = handler.onGetUserNames(peer, task, r)

	case service == svcTitleUtilities && task == 10:
		reply = handler.onGetUserIDs(peer, task, r)

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *storageHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r, payload := request.Service, request.Task, request.Args, request.Payload
	var reply []byte
	switch {
	case service == svcStorage && task == taskGetPublisherFile:
		ctx, err1 := r.String()
		name, err2 := r.String()
		if err1 != nil || err2 != nil {
			peer.Logf("getPublisherFile: unreadable arguments: %X", payload)
			reply = handler.taskReply(task, errUnhandled, nil)
			break
		}
		data, path := handler.publisherFile(peer, name)
		if data == nil {
			// No result rather than an error: the game treats a "missing
			// file" as a normal case.
			reply = handler.taskReply(task, 0, nil)
			peer.Logf("task bdStorage.getPublisherFile ctx=%q file=%q -> MISSING", ctx, name)
			break
		}
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Blob(data); return 1 })
		peer.Logf("task bdStorage.getPublisherFile ctx=%q file=%q -> %s (%d bytes)", ctx, name, path, len(data))

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *userDataHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 29 && (task == 1 || task == 4):
		if reply = handler.onUserData(peer, task, r); reply == nil {
			reply = handler.taskReply(task, 0, nil)
		}

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *contextHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 138:
		ctx, err := r.String()
		if err != nil || ctx == "" {
			reply = handler.taskReply(task, errUnhandled, nil)
			break
		}
		if task == mmCreateSession || task == mmUpdateSession || task == mmDeleteSession || task == mmUpdatePlayers || task == mmRequestSessionID || task == mmInitializeSession {
			reply = handler.onMatchMakingContext(peer, task, r, "ctr:"+ctx)
		} else if task == 14 {
			reply = handler.onFriendSessions(peer, task, r, "ctr:"+ctx)
		} else {
			peer.Logf("CTR matchmaking unimplemented task=%d context=%q", task, ctx)
			reply = handler.taskReply(task, errUnhandled, nil)
		}

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *richPresenceHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == svcRichPresence:
		reply = handler.onRichPresence(peer, task, r)

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *asyncHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == svcAsyncMatchMaking && task == taskSetPlayerInfo:
		// bdAsyncMatchMaking::setPlayerInfo(const char*, u32): the game uploads its
		// listen_server{local_address,security_id,security_key} here. The function has no
		// result, so an empty success is the right answer. It is the FIRST task of the
		// matchmaking registration: failing it brings down everything after it.
		if doc, err := r.String(); err == nil && doc != "" {
			handler.setPlayerInfo(peer.ConnID(), doc)
		}
		reply = handler.taskReply(task, 0, nil)
		peer.Logf("task bdAsyncMatchMaking.setPlayerInfo -> success (%d bytes)", len(handler.playerInfo(peer.ConnID())))

	case service == svcAsyncMatchMaking && task == taskSyncLobbyDocuments:
		id, e1 := r.Uint64()
		version, e2 := r.Uint64()
		raw, e3 := r.String()
		ok := false
		if e1 == nil && e2 == nil && e3 == nil {
			var notices []ctrNotice
			ok, notices = handler.matchmaker.syncDoc(peer, id, version, raw)
			result.AfterReply = func(sendErr error) { sendCTRNotices(notices) }
		}
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Bool(ok); return 1 })
		peer.Logf("CTR sync lobby=%d version=%d accepted=%v", id, version, ok)

	case service == svcAsyncMatchMaking && task == taskGetPlayerToken:
		// getMatchMakingPlayerToken -> bdUInt64Result (readUInt64). This is the
		// requestPlayerToken step of CNetworkTaskAsyncMatchMakingRegistration: without a token the
		// registration state machine cannot advance.
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Uint64(peer.PID()); return 1 })
		peer.Logf("task bdAsyncMatchMaking.getMatchMakingPlayerToken -> %d", peer.PID())

	case service == svcAsyncMatchMaking && task == taskStartSearch:
		raw, err := r.String()
		if err != nil {
			reply = handler.taskReply(task, errUnhandled, nil)
			break
		}
		search, err := handler.matchmaker.parseCTRSearch(peer, raw, handler.playerInfo(peer.ConnID()))
		if err == nil {
			err = handler.matchmaker.reserve(search)
		}
		if err != nil {
			peer.Logf("CTR search rejected: %v", err)
			reply = handler.taskReply(task, errUnhandled, nil)
			break
		}
		doc := "{\"mm_id\":" + strconv.FormatUint(search.id, 10) + "}"
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.String(doc); return 1 })
		result.AfterReply = func(sendErr error) {
			if sendErr != nil {
				handler.matchmaker.release(search) // the client never learned of this search
				return
			}
			notices := handler.matchmaker.activate(search)
			peer.Logf("CTR search id=%d registered; seats=%d filter=%s notifications=%d",
				search.id, search.seats, search.filterID, len(notices))
			sendCTRNotices(notices)
		}

	case service == svcAsyncMatchMaking && task == 8:
		id, err := r.Uint64()
		ok := err == nil && handler.matchmaker.cancel(peer, id)
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Bool(ok); return 1 })
	case service == svcAsyncMatchMaking && task == taskLobbyDisbanded:
		id, err := r.Uint64()
		if err == nil {
			notices := handler.matchmaker.leave(peer, id)
			result.AfterReply = func(sendErr error) { sendCTRNotices(notices) }
		}
		reply = handler.taskReply(task, 0, nil)

	case service == svcAsyncMatchMaking &&
		(task == taskInitMatchMaking || task == taskStartMatchMaking ||
			task == taskGetMMStatus || task == taskInitiateDCQoS):
		// All of these return a bdStringResult (readString).
		id := strconv.FormatUint(peer.PID(), 10)
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.String(id); return 1 })
		peer.Logf("task bdAsyncMatchMaking task=%d -> string %q", task, id)

	case service == svcAsyncMatchMaking &&
		(task == taskQoSHostsReply || task == taskAckExpectGame):
		// No result.
		reply = handler.taskReply(task, 0, nil)
		peer.Logf("task bdAsyncMatchMaking task=%d -> empty success", task)

	case service == svcAsyncMatchMaking && task == taskGetLobbyDocuments:
		id, err := r.Uint64()
		h, b, ok := handler.matchmaker.documents(peer, id)
		if err != nil || !ok {
			reply = handler.taskReply(task, errUnhandled, nil)
			break
		}
		reply = handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.String(h); w.String(b); return 1 })

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *httpProxyHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task := request.Service, request.Task
	var reply []byte
	switch {
	case service == svcHTTPProxy && task == 1:
		// bdABTesting::enroll. The body is parsed as JSON: expiresIn, ABToken,
		// enrollments. No experiment is active, but the reply must be well
		// formed, or the task fails on every connection.
		// ABToken NON-EMPTY: bdABTestingEnrollResponse reads it with getString, and an empty string
		// fails deserialization -> bdRemoteTask error 4 -> the login sequence fails.
		body := []byte(`{"expiresIn":3600,"ABToken":"nextendo","enrollments":[]}`)
		reply = handler.httpProxyReply(task, 200, body)
		peer.Logf("task bdABTesting.enroll -> 200 (%d bytes)", len(body))

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *newsHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == svcMarketingComms:
		reply = handler.onMarketingComms(peer, task, r)

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *economyHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, payload := request.Service, request.Task, request.Payload
	var reply []byte
	switch {
	case service == svcAchievements:
		result = handler.onAchievements(peer, task, payload)
		reply = result.Reply

	case service == svcMarketplace:
		result = handler.onMarketplace(peer, task, payload)
		reply = result.Reply
		if reply == nil {
			reply = handler.taskReply(task, errUnhandled, nil)
			peer.Logf("task marketplace UNHANDLED task=%d args:\n%s", task, hex.Dump(payload))
		}

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *diabloMatchHandler) Handle(peer taskSession, request taskRequest) (result taskResult) {
	service, task, r, payload := request.Service, request.Task, request.Args, request.Payload
	var reply []byte
	switch {
	case service == svcMatchMaking:
		if reply = handler.onMatchMaking(peer, task, r); reply == nil {
			reply = handler.taskReply(task, 0, nil)
			peer.Logf("task matchmaking UNHANDLED task=%d args:\n%s", task, hex.Dump(payload))
		}

	default:
		return defaultTask(handler.replyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func newCTRServiceTable(replies *replyCounter, ctr *ctrServices, presence *presenceStore, registry *sessionRegistry, matchmaker *ctrMatchmaker, files *publisherFiles, pubDir string, relay *relayTable) (*serviceTable, *asyncHandler) {
	friends := &presenceHandler{replyCounter: replies, presence: presence}
	economy := &economyHandler{replyCounter: replies, economy: ctr.economy}
	context := &contextHandler{replyCounter: replies, registry: registry, presence: presence, relay: relay}
	async := &asyncHandler{replyCounter: replies, matchmaker: matchmaker, info: map[uint64]string{}}
	table := &serviceTable{replies: replies, handlers: map[byte]taskHandler{
		4:   &statsHandler{replyCounter: replies, stats: ctr.stats, presence: presence},
		10:  &storageHandler{replyCounter: replies, files: files, pubDir: pubDir},
		12:  &utilityHandler{friends},
		21:  &diabloMatchHandler{context},
		29:  &userDataHandler{friends},
		38:  &platformHandler{replies},
		68:  &richPresenceHandler{friends},
		80:  economy,
		104: &newsHandler{replyCounter: replies, news: ctr.news},
		125: economy,
		138: context,
		145: async,
		194: &httpProxyHandler{replies},
	}}
	table.closeSession = func(peer taskSession) {
		defer registry.dropSessionsOf(peer.ConnID())
		defer async.forget(peer.ConnID())
		sendCTRNotices(matchmaker.leave(peer, 0))
	}
	return table, async
}
