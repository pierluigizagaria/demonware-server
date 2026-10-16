package ctr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/pierluigizagaria/demonware-server/internal/companion"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/strictjson"
)

const advertisementContext = "ctr-advertisement-v1"

type friendAdvertisement struct {
	Version int                 `json:"v"`
	Owner   string              `json:"owner"`
	Context string              `json:"context"`
	Session lobby.Advertisement `json:"session"`
}

type friendAdvertisementHandler struct {
	backend  *P2PBackend
	fallback *contextHandler
	mu       sync.Mutex
	conn     uint64
}

func validAdvertisement(a friendAdvertisement) bool {
	s := a.Session
	return a.Version == 1 && validDecimal(a.Owner) && a.Context == "Delta" && len(s.Address) == 67 && len(s.SessionID) == 8 && s.MaxPlayers > 0 && s.MaxPlayers <= 8 && s.NumPlayers <= s.MaxPlayers && len(s.Attributes) <= 2048
}

func (h *friendAdvertisementHandler) Handle(peer lobby.TaskSession, request lobby.TaskRequest) lobby.TaskResult {
	b := h.backend
	if peer.PID() != b.owner {
		return b.failure(request.Task)
	}
	args := append([]byte(nil), request.Args.Remaining()...)
	r := demonware.NewReader(args)
	ctx, err := r.String()
	if err != nil || ctx != "Delta" {
		return b.failure(request.Task)
	}
	h.mu.Lock()
	if h.conn > peer.ConnID() {
		h.mu.Unlock()
		return b.failure(request.Task)
	}
	h.conn = peer.ConnID()
	h.mu.Unlock()
	if request.Task == 14 {
		return h.lookup(peer, r)
	}
	result := h.fallback.Handle(peer, lobby.TaskRequest{Service: 138, Task: request.Task, Args: demonware.NewReader(args), Payload: args})
	if !successfulReply(result.Reply) {
		return result
	}
	after := result.AfterReply
	result.AfterReply = func(err error) {
		if after != nil {
			after(err)
		}
		if err == nil {
			h.publish(peer)
		}
	}
	return result
}

func (h *friendAdvertisementHandler) publish(peer lobby.TaskSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != peer.ConnID() {
		return
	}
	payload := ""
	if session, ok := h.fallback.Registry.OwnAdvertisement(peer.ConnID(), peer.PID(), "ctr:Delta"); ok {
		a := friendAdvertisement{Version: 1, Owner: strconv.FormatUint(peer.PID(), 10), Context: "Delta", Session: session}
		if !validAdvertisement(a) {
			return
		}
		if !h.backend.canonical.IsValid() || validateCanonicalCommonAddress(session.Address, h.backend.canonical) {
			raw, err := json.Marshal(a)
			if err != nil || len(raw) > 4096 {
				return
			}
			payload = base64.StdEncoding.EncodeToString(raw)
		}
	}
	_ = h.backend.transport.Call(h.backend.ctx, "advertisement.set", map[string]any{"context": advertisementContext, "payload": payload}, nil)
}

func (h *friendAdvertisementHandler) CloseSession(peer lobby.TaskSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != peer.ConnID() {
		return
	}
	h.conn = 0
	_ = h.backend.transport.Call(h.backend.ctx, "advertisement.set", map[string]any{"context": advertisementContext, "payload": ""}, nil)
}

func (h *friendAdvertisementHandler) lookup(peer lobby.TaskSession, r *demonware.Reader) lobby.TaskResult {
	b := h.backend
	ids := r.Uint64s()
	if len(ids) > 100 {
		return b.failure(14)
	}
	ctx, cancel := context.WithTimeout(b.ctx, companion.RequestLimit)
	defer cancel()
	found := []lobby.Advertisement{}
	seen := map[uint64]bool{}
	for _, id := range ids {
		if id == 0 || id == peer.PID() || seen[id] {
			continue
		}
		seen[id] = true
		pid := strconv.FormatUint(id, 10)
		var result struct {
			Payload string `json:"payload"`
		}
		if b.transport.Call(ctx, "advertisement.get", map[string]any{"peer": pid, "context": advertisementContext}, &result) != nil {
			continue
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(result.Payload)
		var ad friendAdvertisement
		if err != nil || len(raw) > 4096 || !strictjson.Decode(string(raw), &ad) || !validAdvertisement(ad) || ad.Owner != pid {
			continue
		}
		var endpoint struct {
			Address string `json:"address"`
			Port    uint16 `json:"port"`
		}
		if b.transport.Call(ctx, "peer.endpoint", map[string]any{"peer": pid, "scope": "friend"}, &endpoint) != nil {
			continue
		}
		address, err := b.peerCommonAddress(base64.StdEncoding.EncodeToString(ad.Session.Address), endpoint.Address, endpoint.Port)
		if err != nil {
			continue
		}
		ad.Session.Address, _ = base64.StdEncoding.DecodeString(address)
		found = append(found, ad.Session)
	}
	return lobby.TaskResult{Reply: b.engine.TaskReply(14, 0, func(w *demonware.Writer) uint32 {
		for _, s := range found {
			w.Blob(s.Address)
			w.Blob(s.SessionID)
			w.Uint32(s.GameType)
			w.Uint32(s.MaxPlayers)
			w.Uint32(s.NumPlayers)
			w.Raw(s.Attributes)
		}
		return uint32(len(found))
	})}
}
