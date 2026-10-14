// Package demonware implements the lobby wire dialect used by this Nextendo
// fork: framing, handshake key derivation, typed arguments and task envelopes.
// It owns no sockets, clocks, identities, service registry or game state.
//
// Callers supply transport reads, transaction/sequence numbers, entropy and
// direction-specific keys. They must authenticate encrypted frames before
// decrypting them and apply their own admission and replay policies.
//
// This is an internal compatibility package, not a complete Demonware SDK.
// See docs/DEMONWARE_PROTOCOL.md for provenance, contracts and support limits.
package demonware
