package lobby

import (
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
)

// TaskSession exposes identity and messaging, never transport keys, sockets or
// persistence. Clock and entropy retain the characterized component test seams.
type TaskSession interface {
	ConnID() uint64
	PID() uint64
	Identity() *platform.PlayerID
	Title() uint32
	Logf(string, ...any)
	SendPush(uint32, func(*demonware.Writer)) error
	Now() time.Time
	Random([]byte) (int, error)
}

type TaskRequest struct {
	Service, Task byte
	Args          *demonware.Reader
	Payload       []byte
}

type TaskHandler interface {
	Handle(TaskSession, TaskRequest) TaskResult
}

func (l *Session) ConnID() uint64 { return l.N }

func (l *Session) Identity() *platform.PlayerID {
	if l.Player == nil {
		return nil
	}
	identity := *l.Player
	return &identity
}

func (l *Session) Title() uint32 {
	if l.Player == nil {
		return 0
	}
	return l.Player.Title
}

func (l *Session) Logf(format string, args ...any) { l.logf(format, args...) }

func (l *Session) SendPush(event uint32, write func(*demonware.Writer)) error {
	return l.sendPush(event, write)
}

func (l *Session) Now() time.Time { return l.now() }

func (l *Session) Random(p []byte) (int, error) { return l.random(p) }

// One immutable table is wired for the instance; routing never selects a game
// table from an untrusted request or from the current session's title.
type ServiceTable struct {
	unreadableTaskErrors [256]bool
	Replies              *ReplyCounter
	Handlers             map[byte]TaskHandler
	OnCloseSession       func(TaskSession)
}

// NewServiceTable copies the unreadable-task policy so callers cannot change
// routing by mutating their configuration slice after construction.
func NewServiceTable(replies *ReplyCounter, handlers map[byte]TaskHandler, unreadableErrors []byte) *ServiceTable {
	table := &ServiceTable{Replies: replies, Handlers: handlers}
	for _, service := range unreadableErrors {
		table.unreadableTaskErrors[service] = true
	}
	return table
}

func (table *ServiceTable) rejectsUnreadableTask(service byte) bool {
	return table != nil && table.unreadableTaskErrors[service]
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
	TaskReply(byte, uint32, func(*demonware.Writer) uint32) []byte
}
