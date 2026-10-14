// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.

package main

import (
	ctrpkg "github.com/pierluigizagaria/demonware-server/internal/ctr"
	diablopkg "github.com/pierluigizagaria/demonware-server/internal/diablo"
	lobbypkg "github.com/pierluigizagaria/demonware-server/internal/lobby"
	relaypkg "github.com/pierluigizagaria/demonware-server/internal/relay"
)

// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.
type lobbyDependencies struct {
	files      *diablopkg.PublisherFiles
	relay      *relaypkg.RelayTable
	matchmaker *ctrpkg.CtrMatchmaker
	registry   *lobbypkg.SessionRegistry
	ctr        *ctrpkg.CtrServices
	pubDir     string
	lobbyDoc   string
}

type lobbyConn struct {
	*lobbypkg.LobbySession
	lobbyDependencies
}

func newTestLobby(session *lobbypkg.LobbySession, dependencies lobbyDependencies) *lobbyConn {
	return &lobbyConn{LobbySession: session, lobbyDependencies: dependencies}
}
