// Online friends.
//
// For verified players, friend IDs are shadNet account IDs. Anonymous guests
// have publisher-assigned IDs and no platform friend association. 12/10
// (getUserIDs) resolves the friends' online names to them; the other lookups
// take them as they are.
//
// Result 12/9 (reader 0xBFCE70, 0x60-byte object): 0A u64 ID | 10 nickname string.
// Result 29/4 (reader 0xBF6860, 0x30-byte object): 0A u64 ID | 13 blob.
//
// We answer for friends currently connected to the lobby. A connection's
// player is the identity its ticket was issued to (tickets.go).

package lobby

// LoadIdentity names the connection's player after the identity its ticket
// was issued to; the handshake has already admitted that ticket.
func (l *Session) LoadIdentity(ticket []byte) {
	id, ok := l.Tickets.IssuedIdentity(ticket)
	if !ok {
		l.logf("identity unknown")
		return
	}
	l.Player = id
	l.Presence.RegisterOnline(l.N, id)
	l.logf("player %q pid=%d", id.Username, id.PID)
}
