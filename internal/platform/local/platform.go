// Package local admits only the owner supplied on the inherited companion pipe.
// It does not claim to verify Steam tickets: the platform library owns Steam.
package local

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/platform/steam"
	"github.com/pierluigizagaria/demonware-server/internal/strictjson"
)

type Owner struct {
	SteamID   string            `json:"steam_id"`
	OnlineID  string            `json:"online_id"`
	Token     string            `json:"token"`
	Title     string            `json:"title"`
	Build     string            `json:"build"`
	Realm     string            `json:"realm"`
	Backend   string            `json:"backend"`
	AppID     *uint32           `json:"app_id,omitempty"`
	Transport *VirtualTransport `json:"transport,omitempty"`
}

// VirtualTransport is an optional, explicitly negotiated canonical endpoint.
// Canonical endpoints never become publisher listeners or forwarding targets.
type VirtualTransport struct {
	Mode        string `json:"mode"`
	Address     string `json:"address"`
	Port        uint16 `json:"port"`
	GameAddress string `json:"game_address"`
	GamePort    uint16 `json:"game_port"`
}

func (t VirtualTransport) Endpoints() (game, canonical netip.AddrPort, err error) {
	ip, e1 := netip.ParseAddr(t.Address)
	localIP, e2 := netip.ParseAddr(t.GameAddress)
	if (t.Mode != "virtual-v1" && t.Mode != "loopback-v1") || e1 != nil || e2 != nil || !ip.Is4() || t.Port == 0 || !localIP.Is4() || !localIP.IsLoopback() || t.GamePort == 0 {
		return game, canonical, errors.New("invalid virtual transport")
	}
	if t.Mode == "loopback-v1" && (!netip.MustParsePrefix("127.64.0.0/10").Contains(ip) || t.Port < 1024 || netip.MustParsePrefix("127.64.0.0/10").Contains(localIP)) {
		return game, canonical, errors.New("invalid loopback transport")
	}
	return netip.AddrPortFrom(localIP, t.GamePort), netip.AddrPortFrom(ip, t.Port), nil
}

type Platform struct {
	mu        sync.RWMutex
	owner     platform.PlayerID
	digest    [32]byte
	trust     string
	weakToken bool
}

// AdmissionError carries only a fixed diagnostic category, never input data.
type AdmissionError uint8

const (
	MalformedEnvelope AdmissionError = iota + 1
	UnsupportedTask
	OwnerCapabilityMismatch
	WeakTokenMalformed
	WeakTokenOwnerMismatch
	WeakTokenNonLoopback
)

func (AdmissionError) Error() string { return "local owner admission refused" }

var buildPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func New(owner Owner) (*Platform, error) {
	return NewWithWeakToken(owner, false)
}

// NewWithWeakToken opts into unauthenticated native token compatibility. The
// HTTP adapter must supply the actual socket source through AuthenticateFrom;
// Authenticate alone never admits a weak token. The pipe owner stays authoritative.
func NewWithWeakToken(owner Owner, weakToken bool) (*Platform, error) {
	appID := uint32(480)
	if owner.AppID != nil {
		appID = *owner.AppID
		if appID == 0 {
			return nil, errors.New("invalid local app ID")
		}
	}
	if owner.Transport != nil {
		if _, _, err := owner.Transport.Endpoints(); err != nil {
			return nil, err
		}
	}
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
		digest: sha256.Sum256([]byte(owner.Token)), trust: "ctrnf:local:" + strconv.FormatUint(uint64(appID), 10) + ":5760:v1:" + owner.Backend + ":" + owner.Realm + ":" + owner.SteamID, weakToken: weakToken}, nil
}

func (p *Platform) TrustID() string { return p.trust }
func (p *Platform) PID() uint64     { return p.Current().PID }
func (p *Platform) Current() platform.PlayerID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.owner
}

// Rename changes only the trusted display label. The exact bootstrap capability,
// account identity and database namespace remain valid for existing sessions.
func (p *Platform) Rename(steamID, name string) (platform.PlayerID, error) {
	valid := len(name) > 0 && len(name) <= 16 && utf8.ValidString(name) && strings.Trim(name, " ") == name
	for _, r := range name {
		valid = valid && r >= 0x20 && r != 0x7f
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !valid || steamID != strconv.FormatUint(p.owner.PID, 10) {
		return platform.PlayerID{}, errors.New("invalid local owner name")
	}
	p.owner.Username = name
	return p.owner, nil
}

func (p *Platform) Authenticate(body []byte) (platform.PlayerID, error) {
	return p.AuthenticateFrom(body, "")
}

// AuthenticateFrom accepts only a direct socket address; forwarded headers
// never establish locality. The capability remains the default credential.
func (p *Platform) AuthenticateFrom(body []byte, remoteAddr string) (platform.PlayerID, error) {
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
		if !p.weakToken || !strings.HasPrefix(value, "sacredfire.") {
			return platform.PlayerID{}, OwnerCapabilityMismatch
		}
		source, err := netip.ParseAddrPort(remoteAddr)
		if err != nil || source.Port() == 0 || !source.Addr().Unmap().IsLoopback() {
			return platform.PlayerID{}, WeakTokenNonLoopback
		}
		account, ok := nativeTokenAccount(value)
		if !ok {
			return platform.PlayerID{}, WeakTokenMalformed
		}
		if account != p.PID() {
			return platform.PlayerID{}, WeakTokenOwnerMismatch
		}
	}
	return p.Current(), nil
}

// Upstream online b1b4bf9 formats sacredfire.<name>.<16 lowercase hex digits>.
// Parse from the right so punctuation in a display name cannot become identity.
// The name is bounded printable ASCII and never overrides the inherited label.
func nativeTokenAccount(value string) (uint64, bool) {
	value, ok := strings.CutPrefix(value, "sacredfire.")
	cut := strings.LastIndexByte(value, '.')
	if !ok || cut < 1 || cut > 16 || len(value)-cut-1 != 16 {
		return 0, false
	}
	for _, c := range []byte(value[:cut]) {
		if c < 0x20 || c > 0x7e {
			return 0, false
		}
	}
	for _, c := range []byte(value[cut+1:]) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return 0, false
		}
	}
	account, err := strconv.ParseUint(value[cut+1:], 16, 64)
	return account, err == nil && account != 0
}

func (p *Platform) AuthData(id platform.PlayerID) map[string]string {
	return map[string]string{"extended_data": "nextendo:local:" + strconv.FormatUint(id.PID, 10)}
}
