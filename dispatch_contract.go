package main

import (
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

// taskSession exposes identity and messaging, never transport keys, sockets or
// persistence. Clock and entropy retain the characterized component test seams.
type taskSession interface {
	ConnID() uint64
	PID() uint64
	Identity() *playerID
	Title() uint32
	Logf(string, ...any)
	SendPush(uint32, func(*demonware.Writer)) error
	Now() time.Time
	Random([]byte) (int, error)
}

type taskRequest struct {
	Service, Task byte
	Args          *demonware.Reader
	Payload       []byte
}

type taskHandler interface {
	Handle(taskSession, taskRequest) taskResult
}

func (l *lobbyConn) ConnID() uint64 { return l.n }
func (l *lobbyConn) PID() uint64    { return l.pid() }
func (l *lobbyConn) Identity() *playerID {
	if l.player == nil {
		return nil
	}
	identity := *l.player
	return &identity
}
func (l *lobbyConn) Title() uint32 {
	if l.player == nil {
		return 0
	}
	return l.player.Title
}
func (l *lobbyConn) Logf(format string, args ...any) { l.logf(format, args...) }
func (l *lobbyConn) SendPush(event uint32, write func(*demonware.Writer)) error {
	return l.sendPush(event, write)
}
func (l *lobbyConn) Now() time.Time               { return l.now() }
func (l *lobbyConn) Random(p []byte) (int, error) { return l.random(p) }

type economyHandler struct {
	*replyCounter
	economy *economyStore
}
type statsHandler struct {
	*replyCounter
	stats    leaderboardStore
	presence *presenceStore
}
type newsHandler struct {
	*replyCounter
	news ctrNewsConfig
}
type presenceHandler struct {
	*replyCounter
	presence *presenceStore
}
type platformHandler struct{ *replyCounter }
