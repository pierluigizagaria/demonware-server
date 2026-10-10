package main

// platformAuthenticator is the boundary between platform admission and common
// Demonware ticket generation. Startup installs exactly one implementation.
type platformAuthenticator interface {
	Authenticate(body []byte, remoteIP string) (playerID, error)
	AuthData(id playerID) map[string]string
}
