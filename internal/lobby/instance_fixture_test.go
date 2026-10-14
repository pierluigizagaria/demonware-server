package lobby

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

func TestInstanceReplyCountersAreIsolated(t *testing.T) {
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.ReplyCounter, second.ReplyCounter = &ReplyCounter{}, &ReplyCounter{}
	for _, l := range []*Session{first, second, first} {
		w := &sessionWire{}
		l.C = w
		l.Table = &ServiceTable{Replies: l.ReplyCounter}
		before := l.S2CSeq
		l.ProcessTask([]byte{255, 3, 1})
		frame, err := demonware.ParseEncryptedFrame(w.Bytes())
		if err != nil || frame.Sequence != before+1 || !frame.Verify(l.Keys.S2CMAC) {
			t.Fatal("invalid reply frame")
		}
		message, err := frame.Decrypt(l.Keys.S2CAES)
		if err != nil || message.Type != demonware.InnerTaskReply {
			t.Fatal("invalid reply payload")
		}
		payload := message.Payload
		transaction, err := demonware.NewReader(payload).Uint64()
		if err != nil || transaction != l.Transactions.Load() {
			t.Fatal("unexpected transaction counter")
		}
	}
	if first.Transactions.Load() != 2 || second.Transactions.Load() != 1 {
		t.Fatal("transaction counters crossed instance boundary")
	}
}

func TestInstanceContextRegistriesAreIsolated(t *testing.T) {
	a, b := NewSessionRegistry(), NewSessionRegistry()
	id := [8]byte{1}
	a.sessions[id] = &MatchSession{ID: id, Owner: 17, OwnerPID: 17}
	b.sessions[id] = &MatchSession{ID: id, Owner: 17, OwnerPID: 17}
	a.DropSessionsOf(17)
	if a.sessions[id].Owner != 0 || b.sessions[id].Owner != 17 {
		t.Fatal("registry disconnect crossed instances")
	}
}
