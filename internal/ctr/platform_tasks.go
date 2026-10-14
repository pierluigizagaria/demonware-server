package ctr

import (
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
)

// Extended identity metadata is parsed after platform admission, without an idle deadline.
func (h *PlatformHandler) OnPlatformIdentityReport(peer lobbypkg.TaskSession, task byte, r *demonwarepkg.Reader) []byte {
	if peer.Identity() == nil || peer.Identity().PID == 0 {
		return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.Uint32(); err != nil {
			return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Uint64(); err != nil {
			return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}
	}
	address, err := r.Blob()
	if err != nil || len(address) != 6 {
		return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
	}
	report, err := r.String()
	if err != nil || report == "" || len(r.Remaining()) != 1 || r.Remaining()[0] != 0 {
		return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
	}
	return h.TaskReply(task, 0, nil)
}
