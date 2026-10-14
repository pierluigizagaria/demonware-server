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
