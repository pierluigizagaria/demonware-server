package diablo

import (
	hex "encoding/hex"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
	relaypkg "github.com/NextendoNetwork/nextendo-demonware/internal/relay"
)

type DiabloMatchHandler struct{ *lobbypkg.ContextHandler }

func (handler *DiabloMatchHandler) Handle(peer lobbypkg.TaskSession, request lobbypkg.TaskRequest) (result lobbypkg.TaskResult) {
	service, task, r, payload := request.Service, request.Task, request.Args, request.Payload
	var reply []byte
	switch {
	case service == lobbypkg.SvcMatchMaking:
		if reply = handler.OnMatchMaking(peer, task, r); reply == nil {
			reply = handler.TaskReply(task, 0, nil)
			peer.Logf("task matchmaking UNHANDLED task=%d args:\n%s", task, hex.Dump(payload))
		}

	default:
		return lobbypkg.DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}

// This compatibility table is intentionally absent from production wiring.
func NewDiabloServiceTable(replies *lobbypkg.ReplyCounter, registry *lobbypkg.SessionRegistry, presence *presencepkg.PresenceStore, relay *relaypkg.RelayTable, files *PublisherFiles) *lobbypkg.ServiceTable {
	return &lobbypkg.ServiceTable{Replies: replies, Handlers: map[byte]lobbypkg.TaskHandler{
		21: &DiabloMatchHandler{&lobbypkg.ContextHandler{ReadInfo: ReadInfo, ReplyCounter: replies, Registry: registry, Presence: presence, Relay: relay}},
		10: &lobbypkg.StorageHandler{ReplyCounter: replies, Files: files},
	}}
}
