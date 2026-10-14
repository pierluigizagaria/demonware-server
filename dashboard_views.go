package main

// The monitoring consumer receives snapshots and never locks its providers.
type sessionSnapshot struct {
	id                               [8]byte
	owner                            uint64
	gameType, maxPlayers, numPlayers uint32
}
type dashboardViews struct {
	online   func(string) map[uint64]playerID
	sessions func() []sessionSnapshot
	rooms    func(map[uint64]string) ([]apiGathering, map[uint64]apiGathering)
}
