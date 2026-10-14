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

var (
	onlineMu sync.Mutex
	online   = map[uint64]*playerID{} // connection number -> player

	userDataMu sync.Mutex
	userData   = map[string][]byte{} // nickname|context -> 29/1 blob
)

// loadIdentity names the connection's player after the identity its ticket
// was issued to; the handshake has already admitted that ticket.
func (l *lobbyConn) loadIdentity(ticket []byte) {
	id, ok := issuedIdentity(ticket)
	if !ok {
		l.logf("identity unknown")
		return
	}
	l.player = id
	onlineMu.Lock()
	online[l.n] = id
	onlineMu.Unlock()
	l.logf("player %q pid=%d", id.Username, id.PID)
}

// dropPresence forgets a player's rich presence (ctr_presence.go keeps the store).
func dropPresence(pid uint64) {
	richMu.Lock()
	delete(rich, pid)
	richMu.Unlock()
}

func dropOnline(conn uint64) {
	onlineMu.Lock()
	var pid uint64
	if p := online[conn]; p != nil {
		pid = p.PID
		for other, q := range online {
			if other != conn && q.PID == pid { // still connected on another connection
				pid = 0
			}
		}
	}
	delete(online, conn)
	onlineMu.Unlock()
	if pid != 0 {
		dropPresence(pid) // a player who left must not look joinable
	}
}

// onlinePlayerFor returns the connected player whose account ID is id, or nil.
func onlinePlayerFor(id uint64) *playerID {
	onlineMu.Lock()
	defer onlineMu.Unlock()
	for _, p := range online {
		if p.PID != 0 && p.PID == id {
			return p
		}
	}
	return nil
}

// onlinePIDs returns every connected player's PID once.
func onlinePIDs() []uint64 {
	onlineMu.Lock()
	defer onlineMu.Unlock()
	seen := map[uint64]bool{}
	out := []uint64{}
	for _, p := range online {
		if p.PID != 0 && !seen[p.PID] {
			seen[p.PID] = true
			out = append(out, p.PID)
		}
	}
	return out
}

// nicknameOnline returns the nickname of a connected player identified by id.
func nicknameOnline(id uint64) string {
	if p := onlinePlayerFor(id); p != nil {
		return p.Username
	}
	return ""
}

// onGetUserNames: 12/9.
func (l *lobbyConn) onGetUserNames(task byte, r *demonware.Reader) []byte {
	ids := r.Uint64s()
	type hit struct {
		id   uint64
		name string
	}
	var hits []hit
	for _, id := range ids {
		if name := nicknameOnline(id); name != "" {
			hits = append(hits, hit{id, name})
		}
	}
	l.logf("friends getUserNames %d request(s) -> %d online %v", len(ids), len(hits), hits)
	return taskReply(task, 0, func(w *demonware.Writer) uint32 {
		for _, h := range hits {
			w.Uint64(h.id)
			w.String(h.name)
		}
		return uint32(len(hits))
	})
}

// onUserData: service 29. 1 = write own data, 4 = read a list's.
func (l *lobbyConn) onUserData(task byte, r *demonware.Reader) []byte {
	ctx, _ := r.String()
	switch task {
	case 1:
		data, err := r.Blob()
		if err != nil || l.player == nil {
			l.logf("userdata SET ctx=%q ignored (%v, player=%v)", ctx, err, l.player != nil)
			return taskReply(task, 0, nil)
		}
		userDataMu.Lock()
		userData[strings.ToLower(l.player.Username)+"|"+ctx] = data
		userDataMu.Unlock()
		l.logf("userdata SET %s ctx=%q %d bytes", l.player.Username, ctx, len(data))
		return taskReply(task, 0, nil)
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
			name := nicknameOnline(id)
			if name == "" {
				continue
			}
			userDataMu.Lock()
			d, ok := userData[strings.ToLower(name)+"|"+ctx]
			userDataMu.Unlock()
			if ok {
				hits = append(hits, hit{id, d})
			}
		}
		l.logf("userdata GET ctx=%q %d request(s) -> %d", ctx, len(ids), len(hits))
		return taskReply(task, 0, func(w *demonware.Writer) uint32 {
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
func pidOnline(id uint64) uint64 {
	if p := onlinePlayerFor(id); p != nil {
		return p.PID
	}
	return 0
}

// onGetUserIDs answers bdTitleUtilities.getUserIDs (12/10): online IDs to
// account IDs. Players connected now win; otherwise the name a player was
// last issued a ticket under still resolves (player_names in ctrnf.db), so
// a friend is found even before they connect to the lobby. Resolving an
// identity does not make it joinable: only live sessions are advertised.
func (l *lobbyConn) onGetUserIDs(task byte, r *demonware.Reader) []byte {
	count, err := r.Uint32()
	if err != nil || count == 0 || count > 500 {
		return taskReply(task, errUnhandled, nil)
	}
	names := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		name, err := r.String()
		if err != nil || name == "" || len(name) > 63 {
			return taskReply(task, errUnhandled, nil)
		}
		names = append(names, name)
	}
	known := map[string]*playerID{}
	if playerNames != nil {
		recorded, err := playerNames.namedPlayers(names)
		if err != nil {
			l.logf("friends getUserIDs: player names: %v", err)
		} else {
			known = recorded
		}
	}
	onlineMu.Lock()
	for _, p := range online {
		known[strings.ToLower(p.Username)] = p
	}
	onlineMu.Unlock()
	var found []*playerID
	for _, name := range names {
		if p := known[strings.ToLower(name)]; p != nil && p.PID != 0 {
			found = append(found, p)
		}
	}
	l.logf("friends getUserIDs %d request(s) -> %d resolved", len(names), len(found))
	return taskReply(task, 0, func(w *demonware.Writer) uint32 {
		for _, p := range found {
			w.Uint64(p.PID)
			w.String(p.Username)
		}
		return uint32(len(found))
	})
}
