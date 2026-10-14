package main

func (registry *sessionRegistry) snapshot() []sessionSnapshot {

	registry.mu.Lock()
	snaps := make([]sessionSnapshot, 0, len(registry.sessions))
	for _, s := range registry.sessions {
		snaps = append(snaps, sessionSnapshot{s.id, s.owner, s.gameType, s.maxPlayers, s.numPlayers})
	}
	registry.mu.Unlock()

	return snaps
}
