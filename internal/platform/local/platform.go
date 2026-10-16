// Package local admits only the owner supplied on the inherited companion pipe.
// It does not claim to verify Steam tickets: the in-process library owns Steam.
package local

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"regexp"
	"strconv"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/platform/steam"
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

var buildPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func New(owner Owner) (*Platform, error) {
	token, err := steam.ParseToken(owner.Token)
	if err != nil || strconv.FormatUint(token.AccountID, 10) != owner.SteamID || token.OnlineID != owner.OnlineID || owner.Title != "5760" || !buildPattern.MatchString(owner.Build) || (owner.Backend != "steam" && owner.Backend != "lab") {
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
	if !ok || subtle.ConstantTimeCompare(digest[:], p.digest[:]) != 1 {
		return platform.PlayerID{}, errors.New("local owner admission refused")
	}
	return p.owner, nil
}

func (p *Platform) AuthData(id platform.PlayerID) map[string]string {
	return map[string]string{"extended_data": "nextendo:local:" + strconv.FormatUint(id.PID, 10)}
}
