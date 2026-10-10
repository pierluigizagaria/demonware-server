package main

// Extended identity metadata is parsed after platform admission, without an idle deadline.
func (l *lobbyConn) onPlatformIdentityReport(task byte, r *bdReader) []byte {
	if l.player == nil || l.player.PID == 0 {
		return taskReply(task, errUnhandled, nil)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.u32(); err != nil {
			return taskReply(task, errUnhandled, nil)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := r.u64(); err != nil {
			return taskReply(task, errUnhandled, nil)
		}
	}
	address, err := r.blob()
	if err != nil || len(address) != 6 {
		return taskReply(task, errUnhandled, nil)
	}
	report, err := r.str()
	if err != nil || report == "" || r.off+1 != len(r.b) || r.b[r.off] != 0 {
		return taskReply(task, errUnhandled, nil)
	}
	return taskReply(task, 0, nil)
}
