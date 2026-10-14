package lobby

import (
	"time"
)

// Publisher storage consumes either CTR physical files or the inactive Diablo
// compatibility provider. The live table never receives the Diablo provider.
type publisherFileSource interface {
	PublisherFile(string, time.Time) ([]byte, string)
}
