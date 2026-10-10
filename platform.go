package main

import "time"

// platformAuthenticator is the boundary between platform admission and common
// Demonware ticket generation. Startup installs exactly one implementation.
type platformAuthenticator interface {
	Authenticate(body []byte, remoteIP string) (playerID, error)
	AuthData(id playerID) map[string]string
	Policy() platformPolicy
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
}

// activePolicy is installed once at startup, before any listener starts.
var activePolicy platformPolicy
