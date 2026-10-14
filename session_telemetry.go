package main

// The session reports transport events without retaining monitoring internals.
type sessionTelemetry interface {
	registerConn(uint64, string)
	unregisterConn(uint64)
	noteTask(uint64, uint64, byte, byte)
}
