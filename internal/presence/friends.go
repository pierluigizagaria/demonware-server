// Online friends.
//
// For verified players, friend IDs are shadNet account IDs. Anonymous guests
// have publisher-assigned IDs and no platform friend association. 12/10
// (getUserIDs) resolves the friends' online names to them; the other lookups
// take them as they are.
//
// Result 12/9 (reader 0xBFCE70, 0x60-byte object): 0A u64 ID | 10 nickname string.
// Result 29/4 (reader 0xBF6860, 0x30-byte object): 0A u64 ID | 13 blob.
//
// We answer for friends currently connected to the lobby. A connection's
// player is the identity its ticket was issued to (tickets.go).

package presence

import (
	"strings"
	"sync"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

type Store struct {
	onlineMu    sync.Mutex
	online      map[uint64]*platform.PlayerID
	userDataMu  sync.Mutex
	userData    map[string][]byte
	richMu      sync.Mutex
	rich        map[uint64]RichPresence
	playerNames playerNameStore
}

func NewStore(names playerNameStore) *Store {
	return &Store{online: map[uint64]*platform.PlayerID{}, userData: map[string][]byte{}, rich: map[uint64]RichPresence{}, playerNames: names}
}

// dropPresence forgets a player's rich presence (ctr_presence.go keeps the store).
func (state *Store) dropPresence(pid uint64) {
	state.richMu.Lock()
	delete(state.rich, pid)
	state.richMu.Unlock()
}

func (state *Store) DropOnline(conn uint64) {
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

// onlinePlayerFor returns the connected player whose account ID is ID, or nil.
func (state *Store) onlinePlayerFor(id uint64) *platform.PlayerID {
	state.onlineMu.Lock()
	defer state.onlineMu.Unlock()
	for _, p := range state.online {
		if p.PID != 0 && p.PID == id {
			return p
		}
	}
	return nil
}

// OnlinePIDs returns every connected player's PID once.
func (state *Store) OnlinePIDs() []uint64 {
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

// NicknameOnline returns the nickname of a connected player identified by ID.
func (state *Store) NicknameOnline(id uint64) string {
	if p := state.onlinePlayerFor(id); p != nil {
		return p.Username
	}
	return ""
}

// OnGetUserNames: 12/9.
func (h *Handler) OnGetUserNames(peer presencePeer, task byte, r *demonware.Reader) []byte {
	ids := r.Uint64s()
	type hit struct {
		id   uint64
		name string
	}
	var hits []hit
	for _, id := range ids {
		if name := h.Presence.NicknameOnline(id); name != "" {
			hits = append(hits, hit{id, name})
		}
	}
	peer.Logf("[presence] friends getUserNames %d request(s) -> %d online %v", len(ids), len(hits), hits)
	return h.TaskReply(task, 0, func(w *demonware.Writer) uint32 {
		for _, h := range hits {
			w.Uint64(h.id)
			w.String(h.name)
		}
		return uint32(len(hits))
	})
}

// OnUserData: service 29. 1 = write own data, 4 = read a list's.
func (h *Handler) OnUserData(peer presencePeer, task byte, r *demonware.Reader) []byte {
	ctx, _ := r.String()
	switch task {
	case 1:
		data, err := r.Blob()
		if err != nil || peer.Identity() == nil {
			peer.Logf("[presence] userdata SET ctx=%q ignored (%v, player=%v)", ctx, err, peer.Identity() != nil)
			return h.TaskReply(task, 0, nil)
		}
		h.Presence.userDataMu.Lock()
		h.Presence.userData[strings.ToLower(peer.Identity().Username)+"|"+ctx] = data
		h.Presence.userDataMu.Unlock()
		peer.Logf("[presence] userdata SET %s ctx=%q %d bytes", peer.Identity().Username, ctx, len(data))
		return h.TaskReply(task, 0, nil)
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
			name := h.Presence.NicknameOnline(id)
			if name == "" {
				continue
			}
			h.Presence.userDataMu.Lock()
			d, ok := h.Presence.userData[strings.ToLower(name)+"|"+ctx]
			h.Presence.userDataMu.Unlock()
			if ok {
				hits = append(hits, hit{id, d})
			}
		}
		peer.Logf("[presence] userdata GET ctx=%q %d request(s) -> %d", ctx, len(ids), len(hits))
		return h.TaskReply(task, 0, func(w *demonware.Writer) uint32 {
			for _, h := range hits {
				w.Uint64(h.id)
				w.Blob(h.data)
			}
			return uint32(len(hits))
		})
	}
	return nil
}

// PIDOnline returns the PID of a connected player identified by ID, or 0.
// Used by the Crash Team Racing code.
func (state *Store) PIDOnline(id uint64) uint64 {
	if p := state.onlinePlayerFor(id); p != nil {
		return p.PID
	}
	return 0
}

// OnGetUserIDs answers bdTitleUtilities.getUserIDs (12/10): online IDs to
// account IDs. Players connected now win; otherwise the name a player was
// last issued a ticket under still resolves (player_names in ctrnf.DB), so
// a friend is found even before they connect to the lobby. Resolving an
// identity does not make it joinable: only live sessions are advertised.
func (h *Handler) OnGetUserIDs(peer presencePeer, task byte, r *demonware.Reader) []byte {
	count, err := r.Uint32()
	if err != nil || count == 0 || count > 500 {
		return h.TaskReply(task, presenceUnhandled, nil)
	}
	names := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		name, err := r.String()
		if err != nil || name == "" || len(name) > 63 {
			return h.TaskReply(task, presenceUnhandled, nil)
		}
		names = append(names, name)
	}
	known := map[string]*platform.PlayerID{}
	if h.Presence.playerNames != nil {
		recorded, err := h.Presence.playerNames.NamedPlayers(names)
		if err != nil {
			peer.Logf("[presence] friends getUserIDs: player names: %v", err)
		} else {
			known = recorded
		}
	}
	h.Presence.onlineMu.Lock()
	for _, p := range h.Presence.online {
		known[strings.ToLower(p.Username)] = p
	}
	h.Presence.onlineMu.Unlock()
	var found []*platform.PlayerID
	for _, name := range names {
		if p := known[strings.ToLower(name)]; p != nil && p.PID != 0 {
			found = append(found, p)
		}
	}
	peer.Logf("[presence] friends getUserIDs %d request(s) -> %d resolved", len(names), len(found))
	return h.TaskReply(task, 0, func(w *demonware.Writer) uint32 {
		for _, p := range found {
			w.Uint64(p.PID)
			w.String(p.Username)
		}
		return uint32(len(found))
	})
}
