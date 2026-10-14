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

func (l *lobbySession) ConnID() uint64 { return l.n }
func (l *lobbySession) PID() uint64    { return l.pid() }
func (l *lobbySession) Identity() *playerID {
	if l.player == nil {
		return nil
	}
	identity := *l.player
	return &identity
}
func (l *lobbySession) Title() uint32 {
	if l.player == nil {
		return 0
	}
	return l.player.Title
}
func (l *lobbySession) Logf(format string, args ...any) { l.logf(format, args...) }
func (l *lobbySession) SendPush(event uint32, write func(*demonware.Writer)) error {
	return l.sendPush(event, write)
}
func (l *lobbySession) Now() time.Time               { return l.now() }
func (l *lobbySession) Random(p []byte) (int, error) { return l.random(p) }

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

// One immutable table is wired for the instance; routing never selects a game
// table from an untrusted request or from the current session's title.
type serviceTable struct {
	replies      *replyCounter
	handlers     map[byte]taskHandler
	closeSession func(taskSession)
}

func (table *serviceTable) Handle(peer taskSession, request taskRequest) taskResult {
	if handler := table.handlers[request.Service]; handler != nil {
		return handler.Handle(peer, request)
	}
	return defaultTask(table.replies, peer, request)
}
func (table *serviceTable) CloseSession(peer taskSession) {
	if table.closeSession != nil {
		table.closeSession(peer)
	}
}
