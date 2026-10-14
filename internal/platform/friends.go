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

package platform

type PlayerID struct {
	Username string `json:"username"`
	PID      uint64 `json:"pid"`
	Sub      string `json:"sub"`
	Kind     string `json:"kind,omitempty"` // "shadnet" or "guest"

	// Demonware title from the ticket: 5760, Crash Team Racing on PS4.
	Title uint32 `json:"-"`
}
