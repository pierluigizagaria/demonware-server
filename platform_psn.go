package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	"github.com/NextendoNetwork/nextendo-demonware/internal/platform/shadnet"
	"github.com/NextendoNetwork/nextendo-demonware/internal/strictjson"
)

// psnPlatform adapts the observed CTR PS4 request envelope to platform identity.
// The supplied verifier determines trust; failed verification has no fallback.
type psnPlatform struct {
	verifier        platform.Verifier
	ticketNamespace string
}

func (p psnPlatform) Authenticate(body []byte, _ string) (playerID, error) {
	refused := func() (playerID, error) { return playerID{}, fmt.Errorf("platform identity refused") }
	var request struct {
		TitleID   string `json:"title_id"`
		AuthTask  string `json:"auth_task"`
		IVSeed    string `json:"iv_seed"`
		ExtraData string `json:"extra_data"`
	}
	if p.verifier == nil || len(body) > 16384 || !decodePSNObject(body, &request, "title_id", "auth_task", "iv_seed", "extra_data") || request.TitleID != "5760" || request.AuthTask != "46" {
		return refused()
	}
	if _, err := strconv.ParseUint(request.IVSeed, 10, 32); err != nil {
		return refused()
	}
	var extra struct {
		Token string `json:"token"`
	}
	var envelope struct {
		IDToken string `json:"id_token"`
	}
	if !decodePSNObject([]byte(request.ExtraData), &extra, "token") || !decodePSNObject([]byte(extra.Token), &envelope, "id_token") || len(envelope.IDToken) == 0 || len(envelope.IDToken) > 4096 {
		return refused()
	}
	id, err := p.verifier.Verify(envelope.IDToken, time.Now())
	if err != nil || id.AccountID == 0 || id.Subject != strconv.FormatUint(id.AccountID, 10) || id.OnlineID == "" {
		return refused()
	}
	// Keep the publisher's subject namespace separate from shadNet's decimal sub.
	return playerID{PID: id.AccountID, Username: id.OnlineID, Sub: "shadnet:" + id.Subject, Kind: "shadnet", Title: 5760}, nil
}

func (psnPlatform) AuthData(id playerID) map[string]string {
	return map[string]string{"extended_data": fmt.Sprintf("nextendo:shadnet:%d", id.PID)}
}

// PS4 credentials expire after 120 seconds, so a publisher ticket admits only
// a fresh connection; established sessions are never cut off afterwards.
func (psnPlatform) Policy() platformPolicy {
	return platformPolicy{IssuedTickets: true, TicketWindow: 120 * time.Second, SignedResponses: true}
}

func loadPSNPlatform(configPath string) (psnPlatform, error) {
	verifier, err := shadnet.Load(configPath)
	if err != nil {
		return psnPlatform{}, err
	}
	return psnPlatform{verifier: verifier, ticketNamespace: "npauth-" + verifier.TrustID()}, nil
}

// Preserve extra game fields but reject duplicate/case-aliased required fields
// so the shared ticket parser and the platform adapter cannot disagree.
func decodePSNObject(raw []byte, out any, required ...string) bool {
	var fields map[string]json.RawMessage
	if !utf8.Valid(raw) || !strictjson.Decode(string(raw), &fields) || fields == nil {
		return false
	}
	for _, name := range required {
		if value, ok := fields[name]; !ok || string(value) == "null" {
			return false
		}
		for key := range fields {
			if key != name && strings.EqualFold(key, name) {
				return false
			}
		}
	}
	return json.Unmarshal(raw, out) == nil
}
