package lobby

import (
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	time "time"
)

// taskSession exposes identity and messaging, never transport keys, sockets or
// persistence. Clock and entropy retain the characterized component test seams.
type TaskSession interface {
	ConnID() uint64
	PID() uint64
	Identity() *platformpkg.PlayerID
	Title() uint32
	Logf(string, ...any)
	SendPush(uint32, func(*demonwarepkg.Writer)) error
	Now() time.Time
	Random([]byte) (int, error)
}

type TaskRequest struct {
	Service, Task byte
	Args          *demonwarepkg.Reader
	Payload       []byte
}

type TaskHandler interface {
	Handle(TaskSession, TaskRequest) TaskResult
}

func (l *LobbySession) ConnID() uint64 { return l.N }

func (l *LobbySession) PID() uint64 { return l.Pid() }

func (l *LobbySession) Identity() *platformpkg.PlayerID {
	if l.Player == nil {
		return nil
	}
	identity := *l.Player
	return &identity
}

func (l *LobbySession) Title() uint32 {
	if l.Player == nil {
		return 0
	}
	return l.Player.Title
}

func (l *LobbySession) Logf(format string, args ...any) { l.logf(format, args...) }

func (l *LobbySession) SendPush(event uint32, write func(*demonwarepkg.Writer)) error {
	return l.sendPush(event, write)
}

func (l *LobbySession) Now() time.Time { return l.now() }

func (l *LobbySession) Random(p []byte) (int, error) { return l.random(p) }

// One immutable table is wired for the instance; routing never selects a game
// table from an untrusted request or from the current session's title.
type ServiceTable struct {
	Replies        *ReplyCounter
	Handlers       map[byte]TaskHandler
	OnCloseSession func(TaskSession)
}

func (table *ServiceTable) Handle(peer TaskSession, request TaskRequest) TaskResult {
	if handler := table.Handlers[request.Service]; handler != nil {
		return handler.Handle(peer, request)
	}
	return DefaultTask(table.Replies, peer, request)
}

func (table *ServiceTable) CloseSession(peer TaskSession) {
	if table.OnCloseSession != nil {
		table.OnCloseSession(peer)
	}
}

type taskReplies interface {
	TaskReply(byte, uint32, func(*demonwarepkg.Writer) uint32) []byte
}
