package lobby

// ChannelSnapshot observes session progress without exposing transport, keys,
// handshake bytes or mutable atomics. Server sends and client progress are
// read under their respective locks, not as a single cross-direction instant.
type ChannelSnapshot struct {
	Established                    bool
	ServerSequence, ClientSequence uint32
}

func (l *Session) ChannelSnapshot() ChannelSnapshot {
	var snapshot ChannelSnapshot
	l.stateMu.Lock()
	snapshot.Established, snapshot.ClientSequence = l.keys != nil, l.c2sSeq
	l.stateMu.Unlock()
	l.sendMu.Lock()
	snapshot.ServerSequence = l.s2cSeq
	l.sendMu.Unlock()
	return snapshot
}
