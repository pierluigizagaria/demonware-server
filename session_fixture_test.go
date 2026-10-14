package main

// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.
type lobbyDependencies struct {
	files      *publisherFiles
	relay      *relayTable
	matchmaker *ctrMatchmaker
	registry   *sessionRegistry
	ctr        *ctrServices
	pubDir     string
	lobbyDoc   string
}
type lobbyConn struct {
	*lobbySession
	lobbyDependencies
}

func newTestLobby(session *lobbySession, dependencies lobbyDependencies) *lobbyConn {
	return &lobbyConn{lobbySession: session, lobbyDependencies: dependencies}
}
