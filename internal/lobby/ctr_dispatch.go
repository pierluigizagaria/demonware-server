package lobby

import (
	"encoding/hex"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
)

type StorageHandler struct {
	*ReplyCounter
	Files publisherFileSource
}

type ContextHandler struct {
	*ReplyCounter
	ReadInfo func(*demonware.Reader) ([]byte, uint32, uint32, []byte, error)
	Registry *SessionRegistry
	Presence *presence.PresenceStore
	Relay    *relay.RelayTable
}

func DefaultTask(replies taskReplies, peer TaskSession, request TaskRequest) TaskResult {
	reply := replies.TaskReply(request.Task, 0, nil)
	peer.Logf("task UNHANDLED service=%d task=%d (empty success) args:\n%s", request.Service, request.Task, hex.Dump(request.Payload))
	return TaskResult{Reply: reply}
}

func (handler *StorageHandler) Handle(peer TaskSession, request TaskRequest) (result TaskResult) {
	service, task, r, payload := request.Service, request.Task, request.Args, request.Payload
	var reply []byte
	switch {
	case service == svcStorage && task == taskGetPublisherFile:
		ctx, err1 := r.String()
		name, err2 := r.String()
		if err1 != nil || err2 != nil {
			peer.Logf("getPublisherFile: unreadable arguments: %X", payload)
			reply = handler.TaskReply(task, ErrUnhandled, nil)
			break
		}
		data, path := handler.Files.PublisherFile(name, peer.Now())
		if data == nil {
			// No result rather than an error: the game treats a "missing
			// file" as a normal case.
			reply = handler.TaskReply(task, 0, nil)
			peer.Logf("task bdStorage.getPublisherFile ctx=%q file=%q -> MISSING", ctx, name)
			break
		}
		reply = handler.TaskReply(task, 0, func(w *demonware.Writer) uint32 { w.Blob(data); return 1 })
		peer.Logf("task bdStorage.getPublisherFile ctx=%q file=%q -> %s (%d bytes)", ctx, name, path, len(data))

	default:
		return DefaultTask(handler.ReplyCounter, peer, request)
	}
	result.Reply = reply
	return result
}
