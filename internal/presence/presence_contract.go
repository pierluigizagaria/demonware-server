package presence

import (
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

const presenceUnhandled = 1

type presencePeer interface {
	PID() uint64
	Identity() *platform.PlayerID
	Logf(string, ...any)
}

type Replies interface {
	TaskReply(byte, uint32, func(*demonware.Writer) uint32) []byte
}
