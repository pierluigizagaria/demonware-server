package main

import "time"

// platformAuthenticator is the boundary between platform admission and common
// Demonware ticket generation. Startup installs exactly one implementation.
type platformAuthenticator interface {
	Authenticate(body []byte, remoteIP string) (playerID, error)
	AuthData(id playerID) map[string]string
	Policy() platformPolicy
	Marketplace() marketplacePlatform
}

// platformPolicy holds the admission rules that differ between platforms.
// The zero value keeps the original Nintendo behavior.
type platformPolicy struct {
	// IssuedTickets limits lobby and Umbrella admission to tickets this
	// server issued and persisted within TicketWindow; a ticket supplied
	// only by the client never provides the handshake key.
	IssuedTickets bool
	TicketWindow  time.Duration
	// SignedResponses refuses authentication when no response signer is
	// configured, instead of returning an unsigned success.
	SignedResponses bool
	// AccountFriendIDs means friend identifiers are already this platform's
	// account IDs: no console aliases or nextendo-account lookups.
	AccountFriendIDs bool
}

// activePolicy is installed once at startup, before any listener starts.
var activePolicy platformPolicy

// Marketplace wire identity is selected from the admitted connection's title,
// never from the client-supplied marketplace context.
type marketplacePlatform struct {
	context     string
	accountType string
	strict      bool // PS4 accepts only the recovered request contracts.
}

func (l *lobbyConn) marketplacePlatform() marketplacePlatform {
	if l.player != nil && l.player.Title == 5760 {
		return (psnPlatform{}).Marketplace()
	}
	return (nintendoPlatform{}).Marketplace()
}
