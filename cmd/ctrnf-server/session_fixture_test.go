// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.

package main

import (
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/diablo"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
)

// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.
type lobbyDependencies struct {
	files      *diablo.PublisherFiles
	relay      *relay.RelayTable
	matchmaker *ctr.CtrMatchmaker
	registry   *lobby.SessionRegistry
	ctr        *ctr.CtrServices
	pubDir     string
	lobbyDoc   string
}

type lobbyConn struct {
	*lobby.LobbySession
	lobbyDependencies
}

func newTestLobby(session *lobby.LobbySession, dependencies lobbyDependencies) *lobbyConn {
	return &lobbyConn{LobbySession: session, lobbyDependencies: dependencies}
}
