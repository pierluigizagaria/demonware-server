// bdRichPresenceService (service 68). Shared by the CTR handlers and the
// inherited Diablo III request fixture; live D3 friend presence is unqualified.
//
//	bdUserAccountID    : 0A u64 id | 10 platform string
//	bdRichPresenceData : bdUserAccountID | 03 u8 | 13 blob
//
// get request: 10 context | 08 u32 count | count x bdUserAccountID.

package presence

import (
	demonwarepkg "github.com/pierluigizagaria/demonware-server/internal/demonware"
)

const SvcRichPresence = 68

const (
	RpSet             = 3
	RpGet             = 4
	RpGetAndSubscribe = 5
	rpUnsubscribe     = 7
)

type accountID struct {
	id       uint64
	platform string
}

type RichPresence struct {
	Platform string
	Flag     byte
	Data     []byte
}

// accountIDs reads "u32 count | count x bdUserAccountID".
func readAccountIDs(r *demonwarepkg.Reader) []accountID {
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
func (state *PresenceStore) onlinePIDSet() map[uint64]bool {
	out := map[uint64]bool{}
	for _, pid := range state.OnlinePIDs() {
		out[pid] = true
	}
	return out
}

func (h *PresenceHandler) OnRichPresence(peer presencePeer, task byte, r *demonwarepkg.Reader) []byte {
	ctx, _ := r.String()
	switch task {
	case RpSet:
		id, err1 := r.Uint64()
		plat, err2 := r.String()
		flag, err3 := r.Uint8()
		data, err4 := r.Blob()
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			peer.Logf("[presence] richpresence SET illisible ctx=%q", ctx)
			return h.TaskReply(task, 0, nil)
		}
		if id == 0 {
			id = peer.PID()
		}
		if id == 0 {
			return h.TaskReply(task, 0, nil)
		}
		h.Presence.RichMu.Lock()
		h.Presence.Rich[id] = RichPresence{plat, flag, data}
		h.Presence.RichMu.Unlock()
		peer.Logf("[presence] richpresence SET pid=%d ctx=%q %d bytes", id, ctx, len(data))
		return h.TaskReply(task, 0, nil)

	case RpGet, RpGetAndSubscribe:
		ids := readAccountIDs(r)
		me := peer.PID()
		live := h.Presence.onlinePIDSet()
		type hit struct {
			id uint64
			p  RichPresence
		}
		var hits []hit
		h.Presence.RichMu.Lock()
		for _, a := range ids {
			id := a.id
			if id == 0 {
				id = me
			}
			// A lookup resolves every requested account, including offline users.
			// Omitting an offline account leaves the caller with no resolved state
			// and can provoke repeated unsubscribe/get-and-subscribe requests.
			// Never return retained join data for a disconnected account.
			p := RichPresence{Platform: a.platform}
			if id == me || live[id] {
				if stored, ok := h.Presence.Rich[id]; ok {
					p = stored
				}
			}
			hits = append(hits, hit{id, p})
		}
		h.Presence.RichMu.Unlock()
		peer.Logf("[presence] richpresence GET ctx=%q %d demande(s) -> %d presence(s)", ctx, len(ids), len(hits))
		return h.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 {
			for _, h := range hits {
				w.Uint64(h.id)
				w.String(h.p.Platform)
				w.Uint8(h.p.Flag)
				w.Blob(h.p.Data)
			}
			return uint32(len(hits))
		})
	}
	return h.TaskReply(task, 0, nil)
}
