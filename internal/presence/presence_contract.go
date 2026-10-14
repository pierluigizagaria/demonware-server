package presence

import (
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
)

const presenceUnhandled = 1

type presencePeer interface {
	PID() uint64
	Identity() *platformpkg.PlayerID
	Logf(string, ...any)
}

type PresenceReplies interface {
	TaskReply(byte, uint32, func(*demonwarepkg.Writer) uint32) []byte
}
