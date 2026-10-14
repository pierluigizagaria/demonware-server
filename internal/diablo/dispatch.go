package diablo

import (
	"encoding/hex"

	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
)

type MatchHandler struct{ *lobby.ContextHandler }

func (handler *MatchHandler) Handle(peer lobby.TaskSession, request lobby.TaskRequest) (result lobby.TaskResult) {
	service, task, r, payload := request.Service, request.Task, request.Args, request.Payload
	var reply []byte
	switch {
	case service == lobby.SvcMatchMaking:
		if reply = handler.OnMatchMaking(peer, task, r); reply == nil {
			reply = handler.TaskReply(task, 0, nil)
			peer.Logf("task matchmaking UNHANDLED task=%d args:\n%s", task, hex.Dump(payload))
		}

	default:
		return lobby.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

// This compatibility table is intentionally absent from production wiring.
func NewServiceTable(replies *lobby.ReplyCounter, registry *lobby.SessionRegistry, presenceStore *presence.Store, relayTable *relay.Table, files *PublisherFiles) *lobby.ServiceTable {
	return &lobby.ServiceTable{Replies: replies, Handlers: map[byte]lobby.TaskHandler{
		21: &MatchHandler{&lobby.ContextHandler{ReadInfo: ReadInfo, ReplyCounter: replies, Registry: registry, Presence: presenceStore, Relay: relayTable}},
		10: &lobby.StorageHandler{ReplyCounter: replies, Files: files},
	}}
}
