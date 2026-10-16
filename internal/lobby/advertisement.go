package lobby

// Advertisement is a copied, live context-session record. It contains only
// room join information already exposed by 138/14, never publisher state.
type Advertisement struct {
	Address    []byte `json:"address"`
	SessionID  []byte `json:"session_id"`
	GameType   uint32 `json:"game_type"`
	MaxPlayers uint32 `json:"max_players"`
	NumPlayers uint32 `json:"num_players"`
	Attributes []byte `json:"attributes"`
}

// OwnAdvertisement never returns a reserved, orphaned or another connection's
// advertisement, even when a stale connection has the same PID.
func (r *SessionRegistry) OwnAdvertisement(conn, pid uint64, context string) (Advertisement, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		if s.Owner != conn || s.OwnerPID != pid || s.Reserved || s.Context != context {
			continue
		}
		return Advertisement{Address: append([]byte(nil), s.hostAddr...), SessionID: append([]byte(nil), s.ID[:]...), GameType: s.gameType, MaxPlayers: s.MaxPlayers, NumPlayers: s.NumPlayers, Attributes: append([]byte(nil), s.Attrs...)}, true
	}
	return Advertisement{}, false
}
