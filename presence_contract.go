package main

import "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"

const presenceUnhandled = 1

type presencePeer interface {
	PID() uint64
	Identity() *playerID
	Logf(string, ...any)
}
type presenceReplies interface {
	taskReply(byte, uint32, func(*demonware.Writer) uint32) []byte
}
