package main

// bdMatchMaking (service 21): public games.
//
// Seen live (Citron, quick match): 21/5 findSessions, then 21/1
// createSession, then 21/2 updateSession. Object format read from D3's
// requests and cross-checked against Ezz-lol/boiii-free's bdMatchMakingInfo:
//
//	createSession (21/1)  : info
//	updateSession (21/2)  : 13 blob sessionID[8] | info
//	deleteSession (21/3)  : 13 blob sessionID[8]
//	updatePlayers (21/12) : 13 blob sessionID[8] | 08 u32 numPlayers | info
//	findSessions  (21/5)  : 08 u32 query | 08 u32 start | 08 u32 max | typed filters
//
//	info (client -> server) : 13 blob hostAddr | 08 u32 gameType | 08 u32 maxPlayers | typed D3 attributes
//	result (server -> client) : 13 blob hostAddr | 13 blob sessionID[8] | 08 u32 gameType
//	                            | 08 u32 maxPlayers | 08 u32 numPlayers | typed D3 attributes
//
// hostAddr (67 bytes) is a bdCommonAddr: local IP + port, relay addresses,
// public IP + port, NAT type. The game uses it to connect P2P to the host;
// the server only relays it.

import (
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

const (
	svcMatchMaking = 21

	mmCreateSession     = 1
	mmUpdateSession     = 2
	mmDeleteSession     = 3
	mmFindSessions      = 5
	mmUpdatePlayers     = 12
	mmRequestSessionID  = 19
	mmInitializeSession = 20
)

type mmSession struct {
	context    string
	id         [8]byte
	owner      uint64 // host's lobby connection number, 0 if disconnected
	ownerPID   uint64
	orphaned   time.Time
	hostAddr   []byte
	gameType   uint32
	maxPlayers uint32
	numPlayers uint32
	attrs      []byte // typed bytes exactly as sent by the host, after maxPlayers
	updated    time.Time
	reserved   bool // ID allocated, but no room information published yet.
}

// orphanGrace: the client re-authenticates in a loop, so a game must survive its
// host reconnecting in order to stay joinable.
const orphanGrace = 2 * time.Minute

func (l *lobbyConn) pid() uint64 {
	if l.player == nil {
		return 0
	}
	return l.player.PID
}

func (handler *contextHandler) owns(peer taskSession, s *mmSession) bool {
	if s.owner == peer.ConnID() {
		return true
	}
	pid := peer.PID()
	return pid != 0 && s.ownerPID == pid
}

// reapSessions removes games whose host never came back.
func (registry *sessionRegistry) reapSessions() {
	for range time.Tick(30 * time.Second) {
		registry.reapSessionsOnce()
	}
}

func (registry *sessionRegistry) reapSessionsOnce() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for id, s := range registry.sessions {
		if s.owner == 0 && time.Since(s.orphaned) > orphanGrace {
			delete(registry.sessions, id)
		}
	}
}

type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[[8]byte]*mmSession
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: map[[8]byte]*mmSession{}}
}

// readInfo reads a bdMatchMakingInfo sent by the client.
func readInfo(r *demonware.Reader) (host []byte, gameType, maxPlayers uint32, attrs []byte, err error) {
	if host, err = r.Blob(); err != nil {
		return
	}
	if gameType, err = r.Uint32(); err != nil {
		return
	}
	if maxPlayers, err = r.Uint32(); err != nil {
		return
	}
	attrs = r.RequestTail()
	return
}

// CTR wraps its title-specific attributes when uploading MatchMakingInfo,
// but its result deserializer reads those fields directly after the base info.
// Trailing request options are outside that blob and are not result fields.
func readContextInfo(r *demonware.Reader, context string) (host []byte, gameType, maxPlayers uint32, attrs []byte, err error) {
	if !strings.HasPrefix(context, "ctr:") {
		return readInfo(r)
	}
	if host, err = r.Blob(); err != nil {
		return
	}
	if gameType, err = r.Uint32(); err != nil {
		return
	}
	if maxPlayers, err = r.Uint32(); err != nil {
		return
	}
	if len(r.Remaining()) > 0 {
		attrs, err = r.Blob()
	}
	return
}

// dropSessionsOf detaches the games of a closed connection; reapSessions removes
// them if the host does not come back.
func (registry *sessionRegistry) dropSessionsOf(conn uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for id, s := range registry.sessions {
		if s.owner != conn {
			continue
		}
		if s.ownerPID == 0 {
			delete(registry.sessions, id)
			continue
		}
		s.owner, s.orphaned = 0, time.Now()
	}
}

// onFriendSessions (138/14): "of these ids, who hosts a game?"
// Without an answer the game shows NOT JOINABLE for every online friend.
func (handler *contextHandler) onFriendSessions(peer taskSession, task byte, r *demonware.Reader, context string) []byte {
	ids := r.Uint64s()
	me := peer.PID()
	seen := map[[8]byte]bool{}
	type hit struct {
		id uint64
		s  *mmSession
	}
	var found []hit
	for _, id := range ids {
		pid := handler.presence.pidOnline(id)
		if pid == 0 {
			// On Citron the friend id IS the PID; the host may be
			// reconnecting and so missing from the online list.
			pid = id
		}
		if pid == me {
			continue
		}
		handler.registry.mu.Lock()
		for _, s := range handler.registry.sessions {
			if s.ownerPID == pid && s.context == context && !s.reserved && !seen[s.id] {
				seen[s.id] = true
				copy := *s
				found = append(found, hit{id, &copy})
			}
		}
		handler.registry.mu.Unlock()
	}
	peer.Logf("matchmaking FRIENDS context=%q %d asked -> %d game(s)", context, len(ids), len(found))
	// The game ties each result to a friend by the online id carried BY the game
	// (Net::MatchMakingInfo +0x170, inside attrs): nothing prefixes the record,
	// which is a raw bdMatchMakingInfo.
	return handler.taskReply(task, 0, func(w *demonware.Writer) uint32 {
		for _, h := range found {
			w.Blob(handler.relay.withRelay(h.s.hostAddr, h.s.ownerPID, peer.PID()))
			w.Blob(h.s.id[:])
			w.Uint32(h.s.gameType)
			w.Uint32(h.s.maxPlayers)
			w.Uint32(h.s.numPlayers)
			w.Raw(h.s.attrs)
		}
		return uint32(len(found))
	})
}

// onMatchMaking handles service 21. Returns nil if the task isn't handled.
func (handler *contextHandler) onMatchMaking(peer taskSession, task byte, r *demonware.Reader) []byte {
	return handler.onMatchMakingContext(peer, task, r, "d3")
}

func (handler *contextHandler) onMatchMakingContext(peer taskSession, task byte, r *demonware.Reader, context string) []byte {
	switch task {
	case mmRequestSessionID:
		s := &mmSession{context: context, owner: peer.ConnID(), ownerPID: peer.PID(), reserved: true, updated: peer.Now()}
		if _, err := peer.Random(s.id[:]); err != nil {
			return handler.taskReply(task, errUnhandled, nil)
		}
		handler.registry.mu.Lock()
		for id, old := range handler.registry.sessions {
			if old.reserved && old.context == context && handler.owns(peer, old) {
				delete(handler.registry.sessions, id)
			}
		}
		handler.registry.sessions[s.id] = s
		handler.registry.mu.Unlock()
		peer.Logf("matchmaking RESERVE session=%X", s.id)
		return handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Blob(s.id[:]); return 1 })
	case mmCreateSession:
		host, gt, maxp, attrs, err := readContextInfo(r, context)
		if err != nil {
			peer.Logf("createSession unreadable: %v", err)
			return handler.taskReply(task, errUnhandled, nil)
		}
		s := &mmSession{owner: peer.ConnID(), ownerPID: peer.PID(), hostAddr: host, gameType: gt, maxPlayers: maxp, numPlayers: 1, attrs: attrs, updated: peer.Now()}
		_, _ = peer.Random(s.id[:])
		s.context = context
		handler.registry.mu.Lock()
		// A host recreating its game after reconnecting replaces the old one.
		if s.ownerPID != 0 {
			for old, prev := range handler.registry.sessions {
				if prev.ownerPID == s.ownerPID && prev.context == context {
					delete(handler.registry.sessions, old)
				}
			}
		}
		handler.registry.sessions[s.id] = s
		n := len(handler.registry.sessions)
		handler.registry.mu.Unlock()
		peer.Logf("matchmaking CREATE session=%X gameType=%d max=%d host=%s (%d open games)",
			s.id, gt, maxp, hex.EncodeToString(host[:min(6, len(host))]), n)
		return handler.taskReply(task, 0, func(w *demonware.Writer) uint32 { w.Blob(s.id[:]); return 1 })

	case mmUpdateSession, mmInitializeSession:
		sid, err := r.Blob()
		if err != nil || len(sid) != 8 {
			peer.Logf("updateSession: id unreadable (%v)", err)
			return handler.taskReply(task, 0, nil)
		}
		host, gt, maxp, attrs, err := readContextInfo(r, context)
		if err != nil {
			peer.Logf("updateSession %X: info unreadable: %v", sid, err)
			return handler.taskReply(task, 0, nil)
		}
		var id [8]byte
		copy(id[:], sid)
		handler.registry.mu.Lock()
		s, ok := handler.registry.sessions[id]
		if ok && s.context == context && handler.owns(peer, s) {
			s.owner, s.ownerPID = peer.ConnID(), peer.PID()
			s.hostAddr, s.gameType, s.maxPlayers, s.attrs, s.updated = host, gt, maxp, attrs, peer.Now()
			if task == mmInitializeSession {
				s.reserved = false
				s.numPlayers = 1
			}
		} else {
			handler.registry.mu.Unlock()
			return handler.taskReply(task, errUnhandled, nil)
		}
		handler.registry.mu.Unlock()
		peer.Logf("matchmaking UPDATE session=%X known=%v", id, ok)
		return handler.taskReply(task, 0, nil)

	case mmUpdatePlayers:
		// Seen live when the console joined Citron's game:
		// 13 blob sessionID | 08 u32 numPlayers | full info.
		sid, err := r.Blob()
		if err != nil || len(sid) != 8 {
			peer.Logf("updateSessionPlayers: id unreadable (%v)", err)
			return handler.taskReply(task, 0, nil)
		}
		players, _ := r.Uint32()
		host, gt, maxp, attrs, err := readContextInfo(r, context)
		var id [8]byte
		copy(id[:], sid)
		handler.registry.mu.Lock()
		s, ok := handler.registry.sessions[id]
		if ok && s.context == context && handler.owns(peer, s) {
			s.owner, s.ownerPID = peer.ConnID(), peer.PID()
			s.numPlayers = players
			if err == nil {
				s.hostAddr, s.gameType, s.maxPlayers, s.attrs = host, gt, maxp, attrs
			}
			s.updated = peer.Now()
		}
		handler.registry.mu.Unlock()
		peer.Logf("matchmaking PLAYERS session=%X players=%d/%d known=%v", id, players, maxp, ok)
		return handler.taskReply(task, 0, nil)

	case mmDeleteSession:
		sid, err := r.Blob()
		if err == nil && len(sid) == 8 {
			var id [8]byte
			copy(id[:], sid)
			handler.registry.mu.Lock()
			if s := handler.registry.sessions[id]; s != nil && s.context == context && handler.owns(peer, s) {
				delete(handler.registry.sessions, id)
			}
			handler.registry.mu.Unlock()
			peer.Logf("matchmaking DELETE session=%X", id)
		}
		return handler.taskReply(task, 0, nil)

	case mmFindSessions:
		query, _ := r.Uint32()
		start, _ := r.Uint32()
		maxr, _ := r.Uint32()
		if maxr == 0 || maxr > 50 {
			maxr = 50
		}
		handler.registry.mu.Lock()
		found := make([]*mmSession, 0, len(handler.registry.sessions))
		for _, s := range handler.registry.sessions {
			if !handler.owns(peer, s) && s.context == context && !s.reserved {
				found = append(found, s)
			}
		}
		handler.registry.mu.Unlock()
		if int(start) < len(found) {
			found = found[start:]
		} else {
			found = nil
		}
		if len(found) > int(maxr) {
			found = found[:maxr]
		}
		peer.Logf("matchmaking FIND query=%d start=%d max=%d -> %d game(s)", query, start, maxr, len(found))
		return handler.taskReply(task, 0, func(w *demonware.Writer) uint32 {
			for _, s := range found {
				w.Blob(handler.relay.withRelay(s.hostAddr, s.ownerPID, peer.PID()))
				w.Blob(s.id[:])
				w.Uint32(s.gameType)
				w.Uint32(s.maxPlayers)
				w.Uint32(s.numPlayers)
				w.Raw(s.attrs)
			}
			return uint32(len(found))
		})
	}
	return nil
}
