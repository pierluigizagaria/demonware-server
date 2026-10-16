package ctr

import (
	"context"
	"github.com/pierluigizagaria/demonware-server/internal/companion"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
)

type localNameHandler struct {
	backend  *P2PBackend
	fallback lobby.TaskHandler
}

func validSteamName(name string) bool {
	if len(name) == 0 || len(name) > 16 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (h *localNameHandler) Handle(peer lobby.TaskSession, request lobby.TaskRequest) lobby.TaskResult {
	b := h.backend
	if request.Task != 9 && request.Task != 10 {
		return h.fallback.Handle(peer, request)
	}
	if peer.PID() != b.owner {
		return b.failure(request.Task)
	}
	type namedPlayer struct {
		SteamID  string `json:"steam_id"`
		OnlineID string `json:"online_id"`
	}
	players := []namedPlayer{}
	ctx, cancel := context.WithTimeout(b.ctx, companion.RequestLimit)
	defer cancel()
	if request.Task == 10 {
		count, err := request.Args.Uint32()
		if err != nil || count == 0 || count > 500 {
			return b.failure(request.Task)
		}
		names := make([]string, 0, count)
		for i := uint32(0); i < count; i++ {
			name, err := request.Args.String()
			if err != nil || !validSteamName(name) {
				return b.failure(request.Task)
			}
			names = append(names, name)
		}
		var result struct {
			Players []namedPlayer `json:"players"`
		}
		if err := b.transport.Call(ctx, "name.lookup", map[string]any{"names": names}, &result); err != nil {
			return b.failure(request.Task)
		}
		if len(result.Players) > 100 {
			return b.failure(request.Task)
		}
		players = result.Players
	} else {
		ids := request.Args.Uint64s()
		if len(ids) > 100 {
			return b.failure(request.Task)
		}
		for _, id := range ids {
			if id == 0 {
				continue
			}
			var result struct {
				OnlineID string `json:"online_id"`
			}
			pid := strconv.FormatUint(id, 10)
			if b.transport.Call(ctx, "name.resolve", map[string]any{"peer": pid}, &result) == nil {
				players = append(players, namedPlayer{pid, result.OnlineID})
			}
		}
	}
	for _, p := range players {
		if !validDecimal(p.SteamID) || !validSteamName(p.OnlineID) {
			return b.failure(request.Task)
		}
	}
	return lobby.TaskResult{Reply: b.engine.TaskReply(request.Task, 0, func(w *demonware.Writer) uint32 {
		for _, p := range players {
			id, _ := strconv.ParseUint(p.SteamID, 10, 64)
			w.Uint64(id)
			w.String(p.OnlineID)
		}
		return uint32(len(players))
	})}
}
