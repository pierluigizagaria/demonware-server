package presence

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

type presenceTestReplies struct{ transaction uint64 }

func (r *presenceTestReplies) TaskReply(task byte, code uint32, write func(*demonware.Writer) uint32) []byte {
	r.transaction++
	return demonware.TaskReply(r.transaction, task, code, write)
}

type presenceTestPeer struct {
	N        uint64
	Player   *platform.PlayerID
	Presence *Store
	replies  *presenceTestReplies
}

func (p *presenceTestPeer) PID() uint64 {
	if p.Player == nil {
		return 0
	}
	return p.Player.PID
}
func (p *presenceTestPeer) Identity() *platform.PlayerID {
	if p.Player == nil {
		return nil
	}
	copy := *p.Player
	return &copy
}
func (*presenceTestPeer) Logf(string, ...any) {}
func (p *presenceTestPeer) onRichPresence(task byte, r *demonware.Reader) []byte {
	return (&Handler{Replies: p.replies, Presence: p.Presence}).OnRichPresence(p, task, r)
}
func withOnline(t *testing.T, state *Store, players map[uint64]*platform.PlayerID) {
	t.Helper()
	state.online = players
	state.rich = map[uint64]RichPresence{}
}
