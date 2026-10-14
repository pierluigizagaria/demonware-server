package main

// bdRichPresenceService (service 68). Shared by the CTR handlers and the
// inherited Diablo III request fixture; live D3 friend presence is unqualified.
//
//	bdUserAccountID    : 0A u64 id | 10 platform string
//	bdRichPresenceData : bdUserAccountID | 03 u8 | 13 blob
//
// get request: 10 context | 08 u32 count | count x bdUserAccountID.

import (
	"sync"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

const svcRichPresence = 68

const (
	rpSet             = 3
	rpGet             = 4
	rpGetAndSubscribe = 5
	rpUnsubscribe     = 7
)

type accountID struct {
	id       uint64
	platform string
}

type richPresence struct {
	platform string
	flag     byte
	data     []byte
}

var (
	richMu sync.Mutex
	rich   = map[uint64]richPresence{}
)

// accountIDs reads "u32 count | count x bdUserAccountID".
func readAccountIDs(r *demonware.Reader) []accountID {
	n, err := r.Uint32()
	if err != nil || n > 4096 {
		return nil
	}
	out := make([]accountID, 0, n)
	for i := uint32(0); i < n; i++ {
		id, err1 := r.Uint64()
		plat, err2 := r.String()
		if err1 != nil || err2 != nil {
			break
		}
		out = append(out, accountID{id, plat})
	}
	return out
}

// onlinePIDSet returns the connected players, indexed for lookup.
func onlinePIDSet() map[uint64]bool {
	out := map[uint64]bool{}
	for _, pid := range onlinePIDs() {
		out[pid] = true
	}
	return out
}

func (l *lobbyConn) onRichPresence(task byte, r *demonware.Reader) []byte {
	ctx, _ := r.String()
	switch task {
	case rpSet:
		id, err1 := r.Uint64()
		plat, err2 := r.String()
		flag, err3 := r.Uint8()
		data, err4 := r.Blob()
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			l.logf("richpresence SET illisible ctx=%q", ctx)
			return taskReply(task, 0, nil)
		}
		if id == 0 {
			id = l.pid()
		}
		if id == 0 {
			return taskReply(task, 0, nil)
		}
		richMu.Lock()
		rich[id] = richPresence{plat, flag, data}
		richMu.Unlock()
		l.logf("richpresence SET pid=%d ctx=%q %d bytes", id, ctx, len(data))
		return taskReply(task, 0, nil)

	case rpGet, rpGetAndSubscribe:
		ids := readAccountIDs(r)
		me := l.pid()
		live := onlinePIDSet()
		type hit struct {
			id uint64
			p  richPresence
		}
		var hits []hit
		richMu.Lock()
		for _, a := range ids {
			id := a.id
			if id == 0 {
				id = me
			}
			// A lookup resolves every requested account, including offline users.
			// Omitting an offline account leaves the caller with no resolved state
			// and can provoke repeated unsubscribe/get-and-subscribe requests.
			// Never return retained join data for a disconnected account.
			p := richPresence{platform: a.platform}
			if id == me || live[id] {
				if stored, ok := rich[id]; ok {
					p = stored
				}
			}
			hits = append(hits, hit{id, p})
		}
		richMu.Unlock()
		l.logf("richpresence GET ctx=%q %d demande(s) -> %d presence(s)", ctx, len(ids), len(hits))
		return taskReply(task, 0, func(w *demonware.Writer) uint32 {
			for _, h := range hits {
				w.Uint64(h.id)
				w.String(h.p.platform)
				w.Uint8(h.p.flag)
				w.Blob(h.p.data)
			}
			return uint32(len(hits))
		})
	}
	return taskReply(task, 0, nil)
}
