// Package platform defines authenticated identity at the publisher boundary.
package platform

import (
	"time"
)

// Identity is produced only after a platform credential has been verified.
type Identity struct {
	AccountID uint64
	OnlineID  string
	Subject   string
}

// Verifier owns platform trust. A credential's lifetime applies to admission,
// not to the lifetime of an already established publisher session.
type Verifier interface {
	Verify(token string, now time.Time) (Identity, error)
}
