package presence

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

func TestPresenceObservationsAreDetached(t *testing.T) {
	state := NewStore(nil)
	identity := &platform.PlayerID{PID: 17, Username: "fixture", Title: 5760}
	state.RegisterOnline(1, identity)
	identity.Username = "changed input"
	view := state.Snapshot("ctr")
	delete(view, 1)
	player := state.ConnectionPlayer(1)
	player.Username = "changed observation"
	if state.NicknameOnline(17) != "fixture" || state.Counts() != (Counts{Online: 1}) {
		t.Fatal("presence ownership escaped")
	}
}
