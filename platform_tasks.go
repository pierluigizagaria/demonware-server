package main

import "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"

// Extended identity metadata is parsed after platform admission, without an idle deadline.
func (h *platformHandler) onPlatformIdentityReport(peer taskSession, task byte, r *demonware.Reader) []byte {
	if peer.Identity() == nil || peer.Identity().PID == 0 {
		return h.taskReply(task, errUnhandled, nil)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.Uint32(); err != nil {
			return h.taskReply(task, errUnhandled, nil)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Uint64(); err != nil {
			return h.taskReply(task, errUnhandled, nil)
		}
	}
	address, err := r.Blob()
	if err != nil || len(address) != 6 {
		return h.taskReply(task, errUnhandled, nil)
	}
	report, err := r.String()
	if err != nil || report == "" || len(r.Remaining()) != 1 || r.Remaining()[0] != 0 {
		return h.taskReply(task, errUnhandled, nil)
	}
	return h.taskReply(task, 0, nil)
}
