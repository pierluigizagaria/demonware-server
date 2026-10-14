package main

// Online friends.
//
// For verified players, friend IDs are shadNet account IDs. Anonymous guests
// have publisher-assigned IDs and no platform friend association. 12/10
// (getUserIDs) resolves the friends' online names to them; the other lookups
// take them as they are.
//
// Result 12/9 (reader 0xBFCE70, 0x60-byte object): 0A u64 id | 10 nickname string.
// Result 29/4 (reader 0xBF6860, 0x30-byte object): 0A u64 id | 13 blob.
//
// We answer for friends currently connected to the lobby. A connection's
// player is the identity its ticket was issued to (tickets.go).

import (
	"strings"
	"sync"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

type playerID struct {
	Username string `json:"username"`
	PID      uint64 `json:"pid"`
	Sub      string `json:"sub"`
	Kind     string `json:"kind,omitempty"` // "shadnet" or "guest"

	// Demonware title from the ticket: 5760, Crash Team Racing on PS4.
	Title uint32 `json:"-"`
}

type presenceStore struct {
	onlineMu    sync.Mutex
	online      map[uint64]*playerID
	userDataMu  sync.Mutex
	userData    map[string][]byte
	richMu      sync.Mutex
	rich        map[uint64]richPresence
	playerNames playerNameStore
}

func newPresenceStore(names playerNameStore) *presenceStore {
	return &presenceStore{online: map[uint64]*playerID{}, userData: map[string][]byte{}, rich: map[uint64]richPresence{}, playerNames: names}
}

// loadIdentity names the connection's player after the identity its ticket
// was issued to; the handshake has already admitted that ticket.
func (l *lobbySession) loadIdentity(ticket []byte) {
	id, ok := l.tickets.issuedIdentity(ticket)
	if !ok {
		l.logf("identity unknown")
		return
	}
	l.player = id
	l.presence.registerOnline(l.n, id)
	l.logf("player %q pid=%d", id.Username, id.PID)
}

// dropPresence forgets a player's rich presence (ctr_presence.go keeps the store).
func (state *presenceStore) dropPresence(pid uint64) {
	state.richMu.Lock()
	delete(state.rich, pid)
	state.richMu.Unlock()
}

func (state *presenceStore) dropOnline(conn uint64) {
	state.onlineMu.Lock()
	var pid uint64
	if p := state.online[conn]; p != nil {
		pid = p.PID
		for other, q := range state.online {
			if other != conn && q.PID == pid { // still connected on another connection
				pid = 0
			}
		}
	}
	delete(state.online, conn)
	state.onlineMu.Unlock()
	if pid != 0 {
		state.dropPresence(pid) // a player who left must not look joinable
	}
}

// onlinePlayerFor returns the connected player whose account ID is id, or nil.
func (state *presenceStore) onlinePlayerFor(id uint64) *playerID {
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	for _, p := range state.online {
		if p.PID != 0 && p.PID == id {
			return p
		}
	}
	return nil
}

// onlinePIDs returns every connected player's PID once.
func (state *presenceStore) onlinePIDs() []uint64 {
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	seen := map[uint64]bool{}
	out := []uint64{}
	for _, p := range state.online {
		if p.PID != 0 && !seen[p.PID] {
			seen[p.PID] = true
			out = append(out, p.PID)
		}
	}
	return out
}

// nicknameOnline returns the nickname of a connected player identified by id.
func (state *presenceStore) nicknameOnline(id uint64) string {
	if p := state.onlinePlayerFor(id); p != nil {
		return p.Username
	}
	return ""
}

// onGetUserNames: 12/9.
func (h *presenceHandler) onGetUserNames(peer presencePeer, task byte, r *demonware.Reader) []byte {
	ids := r.Uint64s()
	type hit struct {
		id   uint64
		name string
	}
	var hits []hit
	for _, id := range ids {
		if name := h.presence.nicknameOnline(id); name != "" {
			hits = append(hits, hit{id, name})
		}
	}
	peer.Logf("friends getUserNames %d request(s) -> %d online %v", len(ids), len(hits), hits)
	return h.taskReply(task, 0, func(w *demonware.Writer) uint32 {
		for _, h := range hits {
			w.Uint64(h.id)
			w.String(h.name)
		}
		return uint32(len(hits))
	})
}

// onUserData: service 29. 1 = write own data, 4 = read a list's.
func (h *presenceHandler) onUserData(peer presencePeer, task byte, r *demonware.Reader) []byte {
	ctx, _ := r.String()
	switch task {
	case 1:
		data, err := r.Blob()
		if err != nil || peer.Identity() == nil {
			peer.Logf("userdata SET ctx=%q ignored (%v, player=%v)", ctx, err, peer.Identity() != nil)
			return h.taskReply(task, 0, nil)
		}
		h.presence.userDataMu.Lock()
		h.presence.userData[strings.ToLower(peer.Identity().Username)+"|"+ctx] = data
		h.presence.userDataMu.Unlock()
		peer.Logf("userdata SET %s ctx=%q %d bytes", peer.Identity().Username, ctx, len(data))
		return h.taskReply(task, 0, nil)
	case 4:
		if len(r.Remaining()) > 0 && r.Remaining()[0] == demonware.TagBool {
			_, _ = r.Bool()
		}
		ids := r.Uint64s()
		type hit struct {
			id   uint64
			data []byte
		}
		var hits []hit
		for _, id := range ids {
			name := h.presence.nicknameOnline(id)
			if name == "" {
				continue
			}
			h.presence.userDataMu.Lock()
			d, ok := h.presence.userData[strings.ToLower(name)+"|"+ctx]
			h.presence.userDataMu.Unlock()
			if ok {
				hits = append(hits, hit{id, d})
			}
		}
		peer.Logf("userdata GET ctx=%q %d request(s) -> %d", ctx, len(ids), len(hits))
		return h.taskReply(task, 0, func(w *demonware.Writer) uint32 {
			for _, h := range hits {
				w.Uint64(h.id)
				w.Blob(h.data)
			}
			return uint32(len(hits))
		})
	}
	return nil
}

// pidOnline returns the PID of a connected player identified by id, or 0.
// Used by the Crash Team Racing code.
func (state *presenceStore) pidOnline(id uint64) uint64 {
	if p := state.onlinePlayerFor(id); p != nil {
		return p.PID
	}
	return 0
}

// onGetUserIDs answers bdTitleUtilities.getUserIDs (12/10): online IDs to
// account IDs. Players connected now win; otherwise the name a player was
// last issued a ticket under still resolves (player_names in ctrnf.db), so
// a friend is found even before they connect to the lobby. Resolving an
// identity does not make it joinable: only live sessions are advertised.
func (h *presenceHandler) onGetUserIDs(peer presencePeer, task byte, r *demonware.Reader) []byte {
	count, err := r.Uint32()
	if err != nil || count == 0 || count > 500 {
		return h.taskReply(task, errUnhandled, nil)
	}
	names := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		name, err := r.String()
		if err != nil || name == "" || len(name) > 63 {
			return h.taskReply(task, errUnhandled, nil)
		}
		names = append(names, name)
	}
	known := map[string]*playerID{}
	if h.presence.playerNames != nil {
		recorded, err := h.presence.playerNames.namedPlayers(names)
		if err != nil {
			peer.Logf("friends getUserIDs: player names: %v", err)
		} else {
			known = recorded
		}
	}
	h.presence.onlineMu.Lock()
	for _, p := range h.presence.online {
		known[strings.ToLower(p.Username)] = p
	}
	h.presence.onlineMu.Unlock()
	var found []*playerID
	for _, name := range names {
		if p := known[strings.ToLower(name)]; p != nil && p.PID != 0 {
			found = append(found, p)
		}
	}
	peer.Logf("friends getUserIDs %d request(s) -> %d resolved", len(names), len(found))
	return h.taskReply(task, 0, func(w *demonware.Writer) uint32 {
		for _, p := range found {
			w.Uint64(p.PID)
			w.String(p.Username)
		}
		return uint32(len(found))
	})
}
