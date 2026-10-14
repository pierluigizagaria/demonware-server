package ps4

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/shadnet"
	"github.com/pierluigizagaria/demonware-server/internal/strictjson"
)

// Crash Team Racing Nitro-Fueled on PS4 (CUSA13795).
const (
	CtrTitle = 5760 // Demonware title
	// PS4 credentials expire after 120 seconds, so a publisher ticket admits
	// only a fresh connection; established sessions are never cut off afterwards.
	TicketWindow = 120 * time.Second
	// Marketplace wire identity of the PS4 build.
	MarketplaceContext     = "octane_psn"
	MarketplaceAccountType = "psn" // PS4 reader 0xb3790 allows at most 10 bytes.
)

func IsCTRTitle(title uint32) bool { return title == CtrTitle }

// psnPlatform adapts the observed CTR PS4 request envelope to platform identity.
// The supplied verifier determines trust; failed verification has no fallback.
type PsnPlatform struct {
	Verifier platform.Verifier
}

func (p PsnPlatform) Authenticate(body []byte) (platform.PlayerID, error) {
	refused := func() (platform.PlayerID, error) {
		return platform.PlayerID{}, fmt.Errorf("platform identity refused")
	}
	token, ok := ReadPSNEnvelope(body)
	if p.Verifier == nil || !ok || token == "" {
		return refused()
	}
	id, err := p.Verifier.Verify(token, time.Now())
	if err != nil || id.AccountID == 0 || id.Subject != strconv.FormatUint(id.AccountID, 10) || id.OnlineID == "" {
		return refused()
	}
	// Keep the publisher's subject namespace separate from shadNet's decimal sub.
	return platform.PlayerID{PID: id.AccountID, Username: id.OnlineID, Sub: "shadnet:" + id.Subject, Kind: "shadnet", Title: CtrTitle}, nil
}

// Both admission providers consume the original PS4 envelope. Only the
// shadNet provider treats its id_token as an account credential.
func ReadPSNEnvelope(body []byte) (string, bool) {
	var request struct {
		TitleID   string `json:"title_id"`
		AuthTask  string `json:"auth_task"`
		IVSeed    string `json:"iv_seed"`
		ExtraData string `json:"extra_data"`
	}
	if len(body) > 16384 || !decodePSNObject(body, &request, "title_id", "auth_task", "iv_seed", "extra_data") || request.TitleID != strconv.Itoa(CtrTitle) || request.AuthTask != "46" {
		return "", false
	}
	if _, err := strconv.ParseUint(request.IVSeed, 10, 32); err != nil {
		return "", false
	}
	var extra struct {
		Token string `json:"token"`
	}
	var envelope struct {
		IDToken string `json:"id_token"`
	}
	if !decodePSNObject([]byte(request.ExtraData), &extra, "token") || !decodePSNObject([]byte(extra.Token), &envelope, "id_token") || len(envelope.IDToken) > 4096 {
		return "", false
	}
	return envelope.IDToken, true
}

func (PsnPlatform) AuthData(id platform.PlayerID) map[string]string {
	return map[string]string{"extended_data": fmt.Sprintf("nextendo:shadnet:%d", id.PID)}
}

// loadPSNPlatform also returns the trust ID (issuer, client and title) that
// the instance database is bound to.
func LoadPSNPlatform(policy shadnet.Policy, jwksURL string) (PsnPlatform, string, error) {
	verifier, err := shadnet.Load(policy, jwksURL)
	if err != nil {
		return PsnPlatform{}, "", err
	}
	return PsnPlatform{Verifier: verifier}, verifier.TrustID(), nil
}

// Preserve extra game fields but reject duplicate/case-aliased required fields
// so the auth handler's request parser and this adapter cannot disagree.
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

func (p PsnPlatform) Close() error {
	if closer, ok := p.Verifier.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
