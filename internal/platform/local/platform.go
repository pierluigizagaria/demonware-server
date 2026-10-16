// Package local admits only the owner supplied on the inherited companion pipe.
// It does not claim to verify Steam tickets: the in-process library owns Steam.
package local

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/platform/steam"
	"github.com/pierluigizagaria/demonware-server/internal/strictjson"
)

type Owner struct {
	SteamID  string `json:"steam_id"`
	OnlineID string `json:"online_id"`
	Token    string `json:"token"`
	Title    string `json:"title"`
	Build    string `json:"build"`
	Realm    string `json:"realm"`
	Backend  string `json:"backend"`
}

type Platform struct {
	owner  platform.PlayerID
	digest [32]byte
	trust  string
}

// AdmissionError carries only a fixed diagnostic category, never input data.
type AdmissionError uint8

const (
	MalformedEnvelope AdmissionError = iota + 1
	UnsupportedTask
	OwnerCapabilityMismatch
)

func (AdmissionError) Error() string { return "local owner admission refused" }

var buildPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func New(owner Owner) (*Platform, error) {
	token, err := steam.ParseToken(owner.Token)
	parts := strings.Split(owner.Token, ".")
	if err != nil || len(parts) != 4 || len(parts[2]) != 64 || strconv.FormatUint(token.AccountID, 10) != owner.SteamID || token.OnlineID != owner.OnlineID || owner.Title != "5760" || !buildPattern.MatchString(owner.Build) || (owner.Backend != "steam" && owner.Backend != "lab") {
		return nil, errors.New("invalid local owner")
	}
	if _, err := steam.TrustID(owner.Realm); err != nil {
		return nil, errors.New("invalid local realm")
	}
	return &Platform{owner: platform.PlayerID{PID: token.AccountID, Username: owner.OnlineID,
		Sub: "local:" + owner.Backend + ":" + owner.SteamID, Kind: "local", Title: ps4.CTRTitle},
		digest: sha256.Sum256([]byte(owner.Token)), trust: "ctrnf:local:480:5760:v1:" + owner.Backend + ":" + owner.Realm + ":" + owner.SteamID}, nil
}

func (p *Platform) TrustID() string { return p.trust }
func (p *Platform) PID() uint64     { return p.owner.PID }

func (p *Platform) Authenticate(body []byte) (platform.PlayerID, error) {
	value, ok := ps4.ReadPSNEnvelope(body)
	digest := sha256.Sum256([]byte(value))
	if !ok {
		// Classification changes no admission rule. In particular, an unknown
		// task is not interpreted as a refresh or supplied another credential.
		var fields map[string]json.RawMessage
		var task string
		if len(body) <= 16384 && strictjson.Decode(string(body), &fields) && json.Unmarshal(fields["auth_task"], &task) == nil && task != "" && task != "46" {
			return platform.PlayerID{}, UnsupportedTask
		}
		return platform.PlayerID{}, MalformedEnvelope
	}
	if subtle.ConstantTimeCompare(digest[:], p.digest[:]) != 1 {
		return platform.PlayerID{}, OwnerCapabilityMismatch
	}
	return p.owner, nil
}

func (p *Platform) AuthData(id platform.PlayerID) map[string]string {
	return map[string]string{"extended_data": "nextendo:local:" + strconv.FormatUint(id.PID, 10)}
}
