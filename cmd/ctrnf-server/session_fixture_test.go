// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.

package main

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/diablo"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
)

// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.
type lobbyDependencies struct {
	files      *diablo.PublisherFiles
	relay      *relay.Table
	matchmaker *ctr.Matchmaker
	registry   *lobby.SessionRegistry
	ctr        *ctr.Services
	pubDir     string
	lobbyDoc   string
}

type lobbyConn struct {
	*lobby.Session
	lobbyDependencies
}

func newTestLobby(session *lobby.Session, dependencies lobbyDependencies) *lobbyConn {
	return &lobbyConn{Session: session, lobbyDependencies: dependencies}
}

func withTestRegistry(t *testing.T) {
	t.Helper()
	old := testRegistry
	testRegistry = lobby.NewSessionRegistry()
	t.Cleanup(func() { testRegistry = old })
}

func observedSession(registry *lobby.SessionRegistry, id [8]byte) *dashboard.SessionSnapshot {
	for _, entry := range registry.Snapshot() {
		if entry.ID == id {
			return &entry
		}
	}
	return nil
}
