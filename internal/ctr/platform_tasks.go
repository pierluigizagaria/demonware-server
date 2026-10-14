package ctr

import (
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
)

// Extended identity metadata is parsed after platform admission, without an idle deadline.
func (h *PlatformHandler) OnPlatformIdentityReport(peer lobby.TaskSession, task byte, r *demonware.Reader) []byte {
	if peer.Identity() == nil || peer.Identity().PID == 0 {
		return h.TaskReply(task, lobby.ErrUnhandled, nil)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.Uint32(); err != nil {
			return h.TaskReply(task, lobby.ErrUnhandled, nil)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Uint64(); err != nil {
			return h.TaskReply(task, lobby.ErrUnhandled, nil)
		}
	}
	address, err := r.Blob()
	if err != nil || len(address) != 6 {
		return h.TaskReply(task, lobby.ErrUnhandled, nil)
	}
	report, err := r.String()
	if err != nil || report == "" || len(r.Remaining()) != 1 || r.Remaining()[0] != 0 {
		return h.TaskReply(task, lobby.ErrUnhandled, nil)
	}
	return h.TaskReply(task, 0, nil)
}
