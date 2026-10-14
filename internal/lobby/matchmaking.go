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

package lobby

import (
	hex "encoding/hex"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	sync "sync"
	time "time"
)

const (
	SvcMatchMaking = 21

	MmCreateSession     = 1
	MmUpdateSession     = 2
	MmDeleteSession     = 3
	mmFindSessions      = 5
	MmUpdatePlayers     = 12
	MmRequestSessionID  = 19
	MmInitializeSession = 20
)

type MmSession struct {
	Context    string
	Id         [8]byte
	Owner      uint64 // host's lobby connection number, 0 if disconnected
	OwnerPID   uint64
	Orphaned   time.Time
	hostAddr   []byte
	gameType   uint32
	MaxPlayers uint32
	NumPlayers uint32
	Attrs      []byte // typed bytes exactly as sent by the host, after maxPlayers
	Updated    time.Time
	Reserved   bool // ID allocated, but no room information published yet.
}

// orphanGrace: the client re-authenticates in a loop, so a game must survive its
// host reconnecting in order to stay joinable.
const OrphanGrace = 2 * time.Minute

func (l *LobbySession) Pid() uint64 {
	if l.Player == nil {
		return 0
	}
	return l.Player.PID
}

func (handler *ContextHandler) Owns(peer TaskSession, s *MmSession) bool {
	if s.Owner == peer.ConnID() {
		return true
	}
	pid := peer.PID()
	return pid != 0 && s.OwnerPID == pid
}

// reapSessions removes games whose host never came back.
func (registry *SessionRegistry) ReapSessions() {
	for range time.Tick(30 * time.Second) {
		registry.ReapSessionsOnce()
	}
}

func (registry *SessionRegistry) ReapSessionsOnce() {
	registry.Mu.Lock()
	defer registry.Mu.Unlock()
	for id, s := range registry.Sessions {
		if s.Owner == 0 && time.Since(s.Orphaned) > OrphanGrace {
			delete(registry.Sessions, id)
		}
	}
}

type SessionRegistry struct {
	Mu       sync.Mutex
	Sessions map[[8]byte]*MmSession
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{Sessions: map[[8]byte]*MmSession{}}
}

// dropSessionsOf detaches the games of a closed connection; reapSessions removes
// them if the host does not come back.
func (registry *SessionRegistry) DropSessionsOf(conn uint64) {
	registry.Mu.Lock()
	defer registry.Mu.Unlock()
	for id, s := range registry.Sessions {
		if s.Owner != conn {
			continue
		}
		if s.OwnerPID == 0 {
			delete(registry.Sessions, id)
			continue
		}
		s.Owner, s.Orphaned = 0, time.Now()
	}
}

// onFriendSessions (138/14): "of these ids, who hosts a game?"
// Without an answer the game shows NOT JOINABLE for every online friend.
func (handler *ContextHandler) OnFriendSessions(peer TaskSession, task byte, r *demonwarepkg.Reader, context string) []byte {
	ids := r.Uint64s()
	me := peer.PID()
	seen := map[[8]byte]bool{}
	type hit struct {
		id uint64
		s  *MmSession
	}
	var found []hit
	for _, id := range ids {
		pid := handler.Presence.PidOnline(id)
		if pid == 0 {
			// On Citron the friend id IS the PID; the host may be
			// reconnecting and so missing from the online list.
			pid = id
		}
		if pid == me {
			continue
		}
		handler.Registry.Mu.Lock()
		for _, s := range handler.Registry.Sessions {
			if s.OwnerPID == pid && s.Context == context && !s.Reserved && !seen[s.Id] {
				seen[s.Id] = true
				copy := *s
				found = append(found, hit{id, &copy})
			}
		}
		handler.Registry.Mu.Unlock()
	}
	peer.Logf("matchmaking FRIENDS context=%q %d asked -> %d game(s)", context, len(ids), len(found))
	// The game ties each result to a friend by the online id carried BY the game
	// (Net::MatchMakingInfo +0x170, inside attrs): nothing prefixes the record,
	// which is a raw bdMatchMakingInfo.
	return handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 {
		for _, h := range found {
			w.Blob(handler.Relay.WithRelay(h.s.hostAddr, h.s.OwnerPID, peer.PID()))
			w.Blob(h.s.Id[:])
			w.Uint32(h.s.gameType)
			w.Uint32(h.s.MaxPlayers)
			w.Uint32(h.s.NumPlayers)
			w.Raw(h.s.Attrs)
		}
		return uint32(len(found))
	})
}

func (handler *ContextHandler) OnMatchMakingContext(peer TaskSession, task byte, r *demonwarepkg.Reader, context string) []byte {
	switch task {
	case MmRequestSessionID:
		s := &MmSession{Context: context, Owner: peer.ConnID(), OwnerPID: peer.PID(), Reserved: true, Updated: peer.Now()}
		if _, err := peer.Random(s.Id[:]); err != nil {
			return handler.TaskReply(task, ErrUnhandled, nil)
		}
		handler.Registry.Mu.Lock()
		for id, old := range handler.Registry.Sessions {
			if old.Reserved && old.Context == context && handler.Owns(peer, old) {
				delete(handler.Registry.Sessions, id)
			}
		}
		handler.Registry.Sessions[s.Id] = s
		handler.Registry.Mu.Unlock()
		peer.Logf("matchmaking RESERVE session=%X", s.Id)
		return handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.Blob(s.Id[:]); return 1 })
	case MmCreateSession:
		host, gt, maxp, attrs, err := handler.ReadInfo(r)
		if err != nil {
			peer.Logf("createSession unreadable: %v", err)
			return handler.TaskReply(task, ErrUnhandled, nil)
		}
		s := &MmSession{Owner: peer.ConnID(), OwnerPID: peer.PID(), hostAddr: host, gameType: gt, MaxPlayers: maxp, NumPlayers: 1, Attrs: attrs, Updated: peer.Now()}
		_, _ = peer.Random(s.Id[:])
		s.Context = context
		handler.Registry.Mu.Lock()
		// A host recreating its game after reconnecting replaces the old one.
		if s.OwnerPID != 0 {
			for old, prev := range handler.Registry.Sessions {
				if prev.OwnerPID == s.OwnerPID && prev.Context == context {
					delete(handler.Registry.Sessions, old)
				}
			}
		}
		handler.Registry.Sessions[s.Id] = s
		n := len(handler.Registry.Sessions)
		handler.Registry.Mu.Unlock()
		peer.Logf("matchmaking CREATE session=%X gameType=%d max=%d host=%s (%d open games)",
			s.Id, gt, maxp, hex.EncodeToString(host[:min(6, len(host))]), n)
		return handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 { w.Blob(s.Id[:]); return 1 })

	case MmUpdateSession, MmInitializeSession:
		sid, err := r.Blob()
		if err != nil || len(sid) != 8 {
			peer.Logf("updateSession: id unreadable (%v)", err)
			return handler.TaskReply(task, 0, nil)
		}
		host, gt, maxp, attrs, err := handler.ReadInfo(r)
		if err != nil {
			peer.Logf("updateSession %X: info unreadable: %v", sid, err)
			return handler.TaskReply(task, 0, nil)
		}
		var id [8]byte
		copy(id[:], sid)
		handler.Registry.Mu.Lock()
		s, ok := handler.Registry.Sessions[id]
		if ok && s.Context == context && handler.Owns(peer, s) {
			s.Owner, s.OwnerPID = peer.ConnID(), peer.PID()
			s.hostAddr, s.gameType, s.MaxPlayers, s.Attrs, s.Updated = host, gt, maxp, attrs, peer.Now()
			if task == MmInitializeSession {
				s.Reserved = false
				s.NumPlayers = 1
			}
		} else {
			handler.Registry.Mu.Unlock()
			return handler.TaskReply(task, ErrUnhandled, nil)
		}
		handler.Registry.Mu.Unlock()
		peer.Logf("matchmaking UPDATE session=%X known=%v", id, ok)
		return handler.TaskReply(task, 0, nil)

	case MmUpdatePlayers:
		// Seen live when the console joined Citron's game:
		// 13 blob sessionID | 08 u32 numPlayers | full info.
		sid, err := r.Blob()
		if err != nil || len(sid) != 8 {
			peer.Logf("updateSessionPlayers: id unreadable (%v)", err)
			return handler.TaskReply(task, 0, nil)
		}
		players, _ := r.Uint32()
		host, gt, maxp, attrs, err := handler.ReadInfo(r)
		var id [8]byte
		copy(id[:], sid)
		handler.Registry.Mu.Lock()
		s, ok := handler.Registry.Sessions[id]
		if ok && s.Context == context && handler.Owns(peer, s) {
			s.Owner, s.OwnerPID = peer.ConnID(), peer.PID()
			s.NumPlayers = players
			if err == nil {
				s.hostAddr, s.gameType, s.MaxPlayers, s.Attrs = host, gt, maxp, attrs
			}
			s.Updated = peer.Now()
		}
		handler.Registry.Mu.Unlock()
		peer.Logf("matchmaking PLAYERS session=%X players=%d/%d known=%v", id, players, maxp, ok)
		return handler.TaskReply(task, 0, nil)

	case MmDeleteSession:
		sid, err := r.Blob()
		if err == nil && len(sid) == 8 {
			var id [8]byte
			copy(id[:], sid)
			handler.Registry.Mu.Lock()
			if s := handler.Registry.Sessions[id]; s != nil && s.Context == context && handler.Owns(peer, s) {
				delete(handler.Registry.Sessions, id)
			}
			handler.Registry.Mu.Unlock()
			peer.Logf("matchmaking DELETE session=%X", id)
		}
		return handler.TaskReply(task, 0, nil)

	case mmFindSessions:
		query, _ := r.Uint32()
		start, _ := r.Uint32()
		maxr, _ := r.Uint32()
		if maxr == 0 || maxr > 50 {
			maxr = 50
		}
		handler.Registry.Mu.Lock()
		found := make([]*MmSession, 0, len(handler.Registry.Sessions))
		for _, s := range handler.Registry.Sessions {
			if !handler.Owns(peer, s) && s.Context == context && !s.Reserved {
				found = append(found, s)
			}
		}
		handler.Registry.Mu.Unlock()
		if int(start) < len(found) {
			found = found[start:]
		} else {
			found = nil
		}
		if len(found) > int(maxr) {
			found = found[:maxr]
		}
		peer.Logf("matchmaking FIND query=%d start=%d max=%d -> %d game(s)", query, start, maxr, len(found))
		return handler.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 {
			for _, s := range found {
				w.Blob(handler.Relay.WithRelay(s.hostAddr, s.OwnerPID, peer.PID()))
				w.Blob(s.Id[:])
				w.Uint32(s.gameType)
				w.Uint32(s.MaxPlayers)
				w.Uint32(s.NumPlayers)
				w.Raw(s.Attrs)
			}
			return uint32(len(found))
		})
	}
	return nil
}
