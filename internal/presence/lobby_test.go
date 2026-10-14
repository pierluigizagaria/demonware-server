package presence

import (
	"encoding/binary"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

// A connected friend's presence comes back; a departed player's does not.
func TestCTRRichPresenceRoundTrip(t *testing.T) {
	testPresence := NewStore(nil)
	testReplies := &presenceTestReplies{}

	host := &presenceTestPeer{replies: testReplies, Presence: testPresence, N: 7771, Player: &platform.PlayerID{Username: "Mars", PID: 1800000119}}
	testPresence.OnlineMu.Lock()
	testPresence.Online[host.N] = host.Player
	testPresence.OnlineMu.Unlock()
	defer testPresence.DropOnline(host.N)

	set := &demonware.Writer{}
	set.String("")
	set.Uint64(1800000119)
	set.String("nintendo")
	set.Uint8(2)
	set.Blob([]byte("racing"))
	host.onRichPresence(RPSet, demonware.NewReader(set.Bytes()))

	ask := func(id uint64) []byte {
		q := &demonware.Writer{}
		q.String("")
		q.Uint32(1)
		q.Uint64(id)
		q.String("nintendo")
		peer := &presenceTestPeer{replies: testReplies, Presence: testPresence, N: 7772, Player: &platform.PlayerID{Username: "Collecting", PID: 1800003406}}
		return peer.onRichPresence(RPGetAndSubscribe, demonware.NewReader(q.Bytes()))
	}
	if n := binary.LittleEndian.Uint32(ask(1800000119)[17:21]); n != 1 {
		t.Fatalf("online friend presence missing: %d results", n)
	}
	if rows, ids := rpRows(t, ask(1800999999)); len(rows) != 1 || ids[0] != 1800999999 || rows[0].Flag != 0 || len(rows[0].Data) != 0 {
		t.Fatalf("offline lookup was not resolved safely: %+v", rows)
	}
}
