// The session reports transport events without retaining monitoring internals.

package lobby

import ()

// The session reports transport events without retaining monitoring internals.
type sessionTelemetry interface {
	RegisterConn(uint64, string)
	UnregisterConn(uint64)
	NoteTask(uint64, uint64, byte, byte)
}
