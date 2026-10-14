package ctr

import (
	hex "encoding/hex"
	demonwarepkg "github.com/pierluigizagaria/demonware-server/internal/demonware"
	lobbypkg "github.com/pierluigizagaria/demonware-server/internal/lobby"
	ps4pkg "github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	presencepkg "github.com/pierluigizagaria/demonware-server/internal/presence"
	relaypkg "github.com/pierluigizagaria/demonware-server/internal/relay"
	strconv "strconv"
	sync "sync"
)

type utilityHandler struct{ *presencepkg.PresenceHandler }

type userDataHandler struct{ *presencepkg.PresenceHandler }

type richPresenceHandler struct{ *presencepkg.PresenceHandler }

type httpProxyHandler struct{ *lobbypkg.ReplyCounter }

type ctrContextHandler struct{ *lobbypkg.ContextHandler }

type asyncHandler struct {
	*lobbypkg.ReplyCounter
	matchmaker *CtrMatchmaker
	mu         sync.Mutex
	info       map[uint64]string
}

func (h *asyncHandler) PlayerInfo(id uint64) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.info[id]
}

func (h *asyncHandler) SetPlayerInfo(id uint64, info string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.info[id] = info
}

func (h *asyncHandler) forget(id uint64) { h.mu.Lock(); defer h.mu.Unlock(); delete(h.info, id) }

func (handler *PlatformHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 38 && task == 6:
		reply = handler.OnPlatformIdentityReport(peer, task, r)

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *StatsHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 4 && peer.Identity() != nil && ps4pkg.IsCTRTitle(peer.Identity().Title):
		reply = handler.OnCTRStats(peer, task, r)

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *utilityHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == lobbypkg.SvcTitleUtilities && task == lobbypkg.TaskGetServerTime:
		now := uint32(peer.Now().Unix())
		reply = handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.Uint32(now); return 1 })
		peer.Logf("[ctr] task bdTitleUtilities.getServerTime -> %d", now)

	case service == lobbypkg.SvcTitleUtilities && task == 9:
		reply = handler.OnGetUserNames(peer, task, r)

	case service == lobbypkg.SvcTitleUtilities && task == 10:
		reply = handler.OnGetUserIDs(peer, task, r)

	default:
		return lobbypkg.DefaultTask(handler.PresenceReplies, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *userDataHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 29 && (task == 1 || task == 4):
		if reply = handler.OnUserData(peer, task, r); reply == nil {
			reply = handler.TaskReply(task, 0, nil)
		}

	default:
		return lobbypkg.DefaultTask(handler.PresenceReplies, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *ctrContextHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == 138:
		ctx, err := r.String()
		if err != nil || ctx == "" {
			reply = handler.TaskReply(task, lobbypkg.ErrUnhandled, nil)
			break
		}
		if task == lobbypkg.MmCreateSession || task == lobbypkg.MmUpdateSession || task == lobbypkg.MmDeleteSession || task == lobbypkg.MmUpdatePlayers || task == lobbypkg.MmRequestSessionID || task == lobbypkg.MmInitializeSession {
			reply = handler.OnMatchMakingContext(peer, task, r, "ctr:"+ctx)
		} else if task == 14 {
			reply = handler.OnFriendSessions(peer, task, r, "ctr:"+ctx)
		} else {
			peer.Logf("[ctr] matchmaking unimplemented task=%d context=%q", task, ctx)
			reply = handler.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *richPresenceHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == presencepkg.SvcRichPresence:
		reply = handler.OnRichPresence(peer, task, r)

	default:
		return lobbypkg.DefaultTask(handler.PresenceReplies, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *asyncHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == lobbypkg.SvcAsyncMatchMaking && task == lobbypkg.TaskSetPlayerInfo:
		// bdAsyncMatchMaking::setPlayerInfo(const char*, u32): the game uploads its
		// listen_server{local_address,security_id,security_key} here. The function has no
		// result, so an empty success is the right answer. It is the FIRST task of the
		// matchmaking registration: failing it brings down everything after it.
		if doc, err := r.String(); err == nil && doc != "" {
			handler.SetPlayerInfo(peer.ConnID(), doc)
		}
		reply = handler.TaskReply(task, 0, nil)
		peer.Logf("[ctr] task bdAsyncMatchMaking.setPlayerInfo -> success (%d bytes)", len(handler.PlayerInfo(peer.ConnID())))

	case service == lobbypkg.SvcAsyncMatchMaking && task == lobbypkg.TaskSyncLobbyDocuments:
		id, e1 := r.Uint64()
		version, e2 := r.Uint64()
		raw, e3 := r.String()
		ok := false
		if e1 == nil && e2 == nil && e3 == nil {
			var notices []ctrNotice
			ok, notices = handler.matchmaker.SyncDoc(peer, id, version, raw)
			result.AfterReply = func(sendErr error) { SendCTRNotices(notices) }
		}
		reply = handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.Bool(ok); return 1 })
		peer.Logf("[ctr] sync lobby=%d version=%d accepted=%v", id, version, ok)

	case service == lobbypkg.SvcAsyncMatchMaking && task == lobbypkg.TaskGetPlayerToken:
		// getMatchMakingPlayerToken -> bdUInt64Result (readUInt64). This is the
		// requestPlayerToken step of CNetworkTaskAsyncMatchMakingRegistration: without a token the
		// registration state machine cannot advance.
		reply = handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.Uint64(peer.PID()); return 1 })
		peer.Logf("[ctr] task bdAsyncMatchMaking.getMatchMakingPlayerToken -> %d", peer.PID())

	case service == lobbypkg.SvcAsyncMatchMaking && task == lobbypkg.TaskStartSearch:
		raw, err := r.String()
		if err != nil {
			reply = handler.TaskReply(task, lobbypkg.ErrUnhandled, nil)
			break
		}
		search, err := handler.matchmaker.ParseCTRSearch(peer, raw, handler.PlayerInfo(peer.ConnID()))
		if err == nil {
			err = handler.matchmaker.Reserve(search)
		}
		if err != nil {
			peer.Logf("[ctr] search rejected: %v", err)
			reply = handler.TaskReply(task, lobbypkg.ErrUnhandled, nil)
			break
		}
		doc := "{\"mm_id\":" + strconv.FormatUint(search.Id, 10) + "}"
		reply = handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.String(doc); return 1 })
		result.AfterReply = func(sendErr error) {
			if sendErr != nil {
				handler.matchmaker.Release(search) // the client never learned of this search
				return
			}
			notices := handler.matchmaker.Activate(search)
			peer.Logf("[ctr] search id=%d registered; seats=%d filter=%s notifications=%d",
				search.Id, search.Seats, search.filterID, len(notices))
			SendCTRNotices(notices)
		}

	case service == lobbypkg.SvcAsyncMatchMaking && task == 8:
		id, err := r.Uint64()
		ok := err == nil && handler.matchmaker.Cancel(peer, id)
		reply = handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.Bool(ok); return 1 })
	case service == lobbypkg.SvcAsyncMatchMaking && task == lobbypkg.TaskLobbyDisbanded:
		id, err := r.Uint64()
		if err == nil {
			notices := handler.matchmaker.Leave(peer, id)
			result.AfterReply = func(sendErr error) { SendCTRNotices(notices) }
		}
		reply = handler.TaskReply(task, 0, nil)

	case service == lobbypkg.SvcAsyncMatchMaking &&
		(task == lobbypkg.TaskInitMatchMaking || task == lobbypkg.TaskStartMatchMaking ||
			task == lobbypkg.TaskGetMMStatus || task == lobbypkg.TaskInitiateDCQoS):
		// All of these return a bdStringResult (readString).
		id := strconv.FormatUint(peer.PID(), 10)
		reply = handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.String(id); return 1 })
		peer.Logf("[ctr] task bdAsyncMatchMaking task=%d -> string %q", task, id)

	case service == lobbypkg.SvcAsyncMatchMaking &&
		(task == lobbypkg.TaskQoSHostsReply || task == lobbypkg.TaskAckExpectGame):
		// No result.
		reply = handler.TaskReply(task, 0, nil)
		peer.Logf("[ctr] task bdAsyncMatchMaking task=%d -> empty success", task)

	case service == lobbypkg.SvcAsyncMatchMaking && task == lobbypkg.TaskGetLobbyDocuments:
		id, err := r.Uint64()
		h, b, ok := handler.matchmaker.Documents(peer, id)
		if err != nil || !ok {
			reply = handler.TaskReply(task, lobbypkg.ErrUnhandled, nil)
			break
		}
		reply = handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.String(h); w.String(b); return 1 })

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *httpProxyHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task := request.Service, request.Task
	var reply []byte
	switch {
	case service == lobbypkg.SvcHTTPProxy && task == 1:
		// bdABTesting::enroll. The body is parsed as JSON: expiresIn, ABToken,
		// enrollments. No experiment is active, but the reply must be well
		// formed, or the task fails on every connection.
		// ABToken NON-EMPTY: bdABTestingEnrollResponse reads it with getString, and an empty string
		// fails deserialization -> bdRemoteTask error 4 -> the login sequence fails.
		body := []byte(`{"expiresIn":3600,"ABToken":"nextendo","enrollments":[]}`)
		reply = handler.HttpProxyReply(task, 200, body)
		peer.Logf("[ctr] task bdABTesting.enroll -> 200 (%d bytes)", len(body))

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *NewsHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r := request.Service, request.Task, request.Args
	var reply []byte
	switch {
	case service == svcMarketingComms:
		reply = handler.OnMarketingComms(peer, task, r)

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func (handler *EconomyHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, payload := request.Service, request.Task, request.Payload
	var reply []byte
	switch {
	case service == svcAchievements:
		result = handler.OnAchievements(peer, task, payload)
		reply = result.Reply

	case service == SvcMarketplace:
		result = handler.OnMarketplace(peer, task, payload)
		reply = result.Reply
		if reply == nil {
			reply = handler.TaskReply(task, lobbypkg.ErrUnhandled, nil)
			peer.Logf("[ctr] task marketplace UNHANDLED task=%d args:\n%s", task, hex.Dump(payload))
		}

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

func NewCTRServiceTable(replies *lobbypkg.ReplyCounter, ctr *CtrServices, presence *presencepkg.PresenceStore, registry *lobbypkg.SessionRegistry, matchmaker *CtrMatchmaker, pubDir string, relay *relaypkg.RelayTable) (*lobbypkg.ServiceTable, *asyncHandler) {
	friends := &presencepkg.PresenceHandler{PresenceReplies: replies, Presence: presence}
	economy := &EconomyHandler{ReplyCounter: replies, Economy: ctr.Economy}
	context := &lobbypkg.ContextHandler{ReadInfo: ReadCTRInfo, ReplyCounter: replies, Registry: registry, Presence: presence, Relay: relay}
	async := &asyncHandler{ReplyCounter: replies, matchmaker: matchmaker, info: map[uint64]string{}}
	table := &lobbypkg.ServiceTable{Replies: replies, Handlers: map[byte]lobbypkg.TaskHandler{
		4:   &StatsHandler{ReplyCounter: replies, Stats: ctr.Stats, Presence: presence},
		10:  &lobbypkg.StorageHandler{ReplyCounter: replies, Files: ctrPublisherFiles{dir: pubDir}},
		12:  &utilityHandler{friends},
		29:  &userDataHandler{friends},
		38:  &PlatformHandler{replies},
		68:  &richPresenceHandler{friends},
		80:  economy,
		104: &NewsHandler{ReplyCounter: replies, News: ctr.News},
		125: economy,
		138: &ctrContextHandler{context},
		145: async,
		194: &httpProxyHandler{replies},
	}}
	table.OnCloseSession = func(peer lobbypkg.TaskSession) {
		defer registry.DropSessionsOf(peer.ConnID())
		defer async.forget(peer.ConnID())
		SendCTRNotices(matchmaker.Leave(peer, 0))
	}
	return table, async
}
