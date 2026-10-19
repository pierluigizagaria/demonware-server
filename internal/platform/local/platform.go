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
	GameAddress string `json:"game_address,omitempty"`
	GamePort    uint16 `json:"game_port,omitempty"`
	GamePending bool   `json:"game_pending,omitempty"`
}

func (t *VirtualTransport) UnmarshalJSON(raw []byte) error {
	type plain VirtualTransport
	var value plain
	var fields map[string]json.RawMessage
	if !strictjson.Decode(string(raw), &fields) || json.Unmarshal(raw, &value) != nil {
		return errors.New("invalid transport descriptor")
	}
	if value.Mode == "loopback-v1" {
		keys := []string{"mode", "address", "port", "game_address", "game_port"}
		if value.GamePending {
			keys = []string{"mode", "address", "port", "game_pending"}
		}
		if len(fields) != len(keys) {
			return errors.New("invalid loopback transport fields")
		}
		for _, key := range keys {
			if _, ok := fields[key]; !ok {
				return errors.New("missing loopback transport field")
			}
		}
	}
	*t = VirtualTransport(value)
	return nil
}

func (t VirtualTransport) Endpoints() (game, canonical netip.AddrPort, err error) {
	ip, e1 := netip.ParseAddr(t.Address)
	if (t.Mode != "virtual-v1" && t.Mode != "loopback-v1") || e1 != nil || !ip.Is4() || t.Port == 0 {
		return game, canonical, errors.New("invalid virtual transport")
	}
	canonical = netip.AddrPortFrom(ip, t.Port)
	if t.Mode == "loopback-v1" && (!netip.MustParsePrefix("127.64.0.0/10").Contains(ip) || t.Port < 1024) {
		return game, canonical, errors.New("invalid loopback transport")
	}
	if t.GamePending {
		if t.Mode != "loopback-v1" || t.GameAddress != "" || t.GamePort != 0 {
			return game, canonical, errors.New("invalid pending game endpoint")
		}
		return game, canonical, nil
	}
	localIP, e2 := netip.ParseAddr(t.GameAddress)
	if e2 != nil || !localIP.Is4() || !localIP.IsLoopback() || t.GamePort == 0 {
		return game, canonical, errors.New("invalid virtual transport")
	}
	if t.Mode == "loopback-v1" && netip.MustParsePrefix("127.64.0.0/10").Contains(localIP) {
		return game, canonical, errors.New("invalid loopback transport")
	}
	return netip.AddrPortFrom(localIP, t.GamePort), netip.AddrPortFrom(ip, t.Port), nil
}

type Platform struct {
	mu        sync.RWMutex
	owner     platform.PlayerID
	digest    [32]byte
	trust     string
	backend   string
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
	if owner.Transport != nil && owner.Transport.Mode == "loopback-v1" && owner.AppID == nil {
		return nil, errors.New("loopback transport requires explicit app ID")
	}
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
		digest: sha256.Sum256([]byte(owner.Token)), trust: "ctrnf:local:v2:" + owner.Backend, backend: owner.Backend, weakToken: weakToken}, nil
}

// TrustID names the persistence namespace: one database per installation and
// backend. Account rows are keyed by SteamID, so other accounts, AppIDs, realms
// and economy modes reuse it; the owner, AppID, realm, title and build are still
// validated for every session above.
func (p *Platform) TrustID() string { return p.trust }

// LegacyTrustID reports whether trust is this backend's v1 database binding,
// ctrnf:local:<app>:5760:v1:<backend>:<realm>:<SteamID>, which pinned one owner,
// AppID and realm. Such a database may be upgraded in place to TrustID.
func (p *Platform) LegacyTrustID(trust string) bool {
	parts := strings.Split(trust, ":")
	canonical := func(text string) bool {
		value, err := strconv.ParseUint(text, 10, 64)
		return err == nil && value != 0 && strconv.FormatUint(value, 10) == text
	}
	if len(parts) != 8 || parts[0] != "ctrnf" || parts[1] != "local" || parts[3] != "5760" || parts[4] != "v1" || parts[5] != p.backend {
		return false
	}
	app, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil || !canonical(parts[2]) || app == 0 || !canonical(parts[7]) {
		return false
	}
	_, err = steam.TrustID(parts[6])
	return err == nil
}

func (p *Platform) PID() uint64 { return p.Current().PID }
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
