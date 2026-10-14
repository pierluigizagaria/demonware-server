// bdMatchMaking (service 21): public games.
//
// Seen live (Citron, quick match): 21/5 findSessions, then 21/1
// createSession, then 21/2 updateSession. Object format read from D3's
// requests and cross-checked against Ezz-lol/boiii-free's bdMatchMakingInfo:
//
//	createSession (21/1)  : info
//	updateSession (21/2)  : 13 blob sessionID[8] | info
//	deleteSession (21/3)  : 13 blob sessionID[8]
//	updatePlayers (21/12) : 13 blob sessionID[8] | 08 u32 numPlayers | info
//	findSessions  (21/5)  : 08 u32 query | 08 u32 start | 08 u32 max | typed filters
//
//	info (client -> server) : 13 blob hostAddr | 08 u32 gameType | 08 u32 maxPlayers | typed D3 attributes
//	result (server -> client) : 13 blob hostAddr | 13 blob sessionID[8] | 08 u32 gameType
//	                            | 08 u32 maxPlayers | 08 u32 numPlayers | typed D3 attributes
//
// hostAddr (67 bytes) is a bdCommonAddr: local IP + port, relay addresses,
// public IP + port, NAT type. The game uses it to connect P2P to the host;
// the server only relays it.

package ctr

import (
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// CTR wraps its title-specific attributes when uploading MatchMakingInfo,
// but its result deserializer reads those fields directly after the base info.
// Trailing request options are outside that blob and are not result fields.
func ReadInfo(r *demonware.Reader) (host []byte, gameType, maxPlayers uint32, attrs []byte, err error) {
	if host, err = r.Blob(); err != nil {
		return
	}
	if gameType, err = r.Uint32(); err != nil {
		return
	}
	if maxPlayers, err = r.Uint32(); err != nil {
		return
	}
	if len(r.Remaining()) > 0 {
		attrs, err = r.Blob()
	}
	return
}
