package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/pierluigizagaria/demonware-server/internal/platform/shadnet"
	"github.com/pierluigizagaria/demonware-server/internal/platform/steam"
)

var settingPrefixes = []string{"CTR_", "D3_", "DASH_", "NEXTENDO_", "SHADNET_"}

// Read by the integration test only, never by the server.
var testOnlySettings = map[string]bool{"SHADNET_INTEROP_FIXTURE": true}

type Config struct {
	PublicIP, BindAddr                    string
	AuthPort, NATPort, DashPort           int
	LobbyPorts                            string
	ProxyProtocol                         bool
	CertFile, KeyFile                     string
	DashToken, SNIHost                    string
	PubfilesConfig, PubfilesDir, Dumps    string
	Verbose                               bool
	RiftData                              string
	Shadnet                               shadnet.Policy
	Admission                             string
	Matchmaking                           string
	P2PTiming                             bool
	AuthHTTP                              bool
	LocalWeakToken                        bool
	Companion                             string
	SteamHelper, SteamLibrary, SteamRealm string
	JWKSURL                               string
	SigningKey, DataDir                   string
	UnlockAll, Migrate, RedactLogs        bool
	StartBalance                          int64
	RelayLow, RelayHigh                   int
	RelayPIDs                             string
	News                                  News
}

// Read loads envFile, if any, and reads the configuration. serving
// requires what the server needs beyond `server pubfiles`.
func Read(envFile string, serving bool) (Config, error) {
	return ReadEnvironment(envFile, serving, os.Environ())
}

// Read the file as an instance overlay. Existing process values (including an
// explicitly empty value) win, without installing file values into the process.
func ReadEnvironment(envFile string, serving bool, environ []string) (Config, error) {
	values := map[string]string{}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if ok {
			values[canonicalName(name)] = value
		}
	}
	names := append([]string(nil), environ...)
	if envFile != "" {
		vars, err := ParseEnvFile(envFile)
		if err != nil {
			return Config{}, err
		}
		for _, v := range vars {
			name := canonicalName(v[0])
			if _, exists := values[name]; exists {
				continue
			}
			if strings.IndexByte(v[1], 0) >= 0 {
				return Config{}, fmt.Errorf("%s: environment value contains NUL", v[0])
			}
			values[name] = v[1]
			names = append(names, name+"="+v[1])
		}
	}
	return Load(func(name string) string { return values[canonicalName(name)] }, SettingNames(names), serving)
}

// SettingNames returns the canonical names in environ, without repeats, that
// carry one of this server's prefixes; the env file's variables are among them.
func SettingNames(environ []string) []string {
	var names []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		name = canonicalName(name)
		for _, prefix := range settingPrefixes {
			if strings.HasPrefix(name, prefix) && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// canonicalName folds case where environment names are case-insensitive.
func canonicalName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// Load reads every setting through lookup. Each name in check, the
// environment's variables with a server prefix, must be a setting.
func Load(lookup func(string) string, check []string, serving bool) (Config, error) {
	r := &envReader{lookup: lookup, known: map[string]bool{}}
	admission := r.text("CTR_ADMISSION", "shadnet")
	if admission != "shadnet" && admission != "guest" && admission != "steam" && admission != "local" {
		r.fail("CTR_ADMISSION must be shadnet, guest, steam or local")
	}
	needsShadNet := serving && admission == "shadnet"
	c := Config{
		Admission:      admission,
		Matchmaking:    r.text("CTR_MATCHMAKING", "central"),
		P2PTiming:      r.flag("CTR_P2P_TIMING", false),
		AuthHTTP:       r.flag("CTR_AUTH_HTTP", false),
		LocalWeakToken: r.flag("CTR_LOCAL_WEAK_TOKEN", false),
		Companion:      r.text("CTR_COMPANION", ""),
		SteamHelper:    r.required("CTR_STEAM_HELPER", serving && admission == "steam"),
		SteamLibrary:   r.text("CTR_STEAM_LIBRARY", ""),
		SteamRealm:     r.text("CTR_STEAM_REALM", ""),
		PublicIP:       r.ipv4("NEXTENDO_HOST", "127.0.0.1"),
		BindAddr:       r.ip("BIND_ADDR"),
		AuthPort:       r.port("AUTH_PORT", 8460),
		LobbyPorts:     r.ports("LOBBY_PORTS", "3074,3075,3076,3077,3078,3079,3080"),
		NATPort:        r.port("NAT_PORT", 3074),
		ProxyProtocol:  r.flag("NEXTENDO_PROXY_PROTOCOL", false),
		CertFile:       r.text("CERT_FILE", "cert.pem"),
		KeyFile:        r.text("KEY_FILE", "key.pem"),
		DashPort:       r.port("DASH_PORT", 8093),
		DashToken:      r.text("DASH_TOKEN", ""),
		SNIHost:        r.text("NEXTENDO_SNI_HOST", ""),
		PubfilesConfig: r.text("D3_PUBFILES_CONFIG", "pubfiles.json"),
		PubfilesDir:    r.text("D3_PUBFILES", "pubfiles"),
		Dumps:          r.text("D3_DUMPS", "off"),
		Verbose:        r.flag("D3_VERBOSE", false),
		RiftData:       r.text("D3_RIFTDATA", "riftdata"),
		Shadnet: shadnet.Policy{
			Issuer:    r.required("SHADNET_ISSUER", needsShadNet),
			ClientID:  r.required("SHADNET_CLIENT_ID", needsShadNet),
			NPTitleID: r.required("SHADNET_TITLE_ID", needsShadNet),
			Scopes:    strings.Fields(r.required("SHADNET_SCOPES", needsShadNet)),
		},
		JWKSURL:      r.required("SHADNET_JWKS_URL", needsShadNet),
		SigningKey:   r.required("CTR_AUTH_SIGNING_KEY", serving),
		DataDir:      r.text("DATA_DIR", "data"),
		UnlockAll:    r.flag("CTR_UNLOCK_ALL", false),
		StartBalance: r.balance("CTR_START_BALANCE"),
		Migrate:      r.flag("CTR_MIGRATE", false),
		RelayLow:     r.integer("CTR_RELAY_PORT_LOW", 0, 0, 65535),
		RelayHigh:    r.integer("CTR_RELAY_PORT_HIGH", 0, 0, 65535),
		RelayPIDs:    r.pids("CTR_RELAY_PIDS"),
		RedactLogs:   r.flag("NEXTENDO_REDACT_LOGS", true),
		News:         r.news(),
	}
	if admission == "guest" || admission == "steam" || admission == "local" {
		if admission == "guest" && !c.UnlockAll {
			r.fail("CTR_ADMISSION=guest requires CTR_UNLOCK_ALL=1")
		}
		if c.Shadnet.Issuer != "" || c.Shadnet.ClientID != "" || c.Shadnet.NPTitleID != "" || len(c.Shadnet.Scopes) != 0 || c.JWKSURL != "" {
			r.fail("SHADNET_* must be empty for %s admission", admission)
		}
	}
	if admission == "steam" {
		if _, err := steam.TrustID(c.SteamRealm); err != nil {
			r.fail("CTR_STEAM_REALM: %v", err)
		}
	} else if c.SteamHelper != "" || c.SteamLibrary != "" || c.SteamRealm != "" {
		r.fail("CTR_STEAM_* must be empty outside steam admission")
	}
	if c.Matchmaking != "central" && c.Matchmaking != "steam" {
		r.fail("CTR_MATCHMAKING must be central or steam")
	}
	if admission == "local" && c.Companion != "stdio" {
		r.fail("CTR_ADMISSION=local requires CTR_COMPANION=stdio")
	} else if admission != "local" && c.Companion != "" {
		r.fail("CTR_COMPANION is only supported with local admission")
	}
	if c.Matchmaking == "steam" && admission != "local" {
		r.fail("CTR_MATCHMAKING=steam requires CTR_ADMISSION=local")
	}
	if c.LocalWeakToken && admission != "local" {
		r.fail("CTR_LOCAL_WEAK_TOKEN requires CTR_ADMISSION=local")
	}
	if admission == "local" || c.AuthHTTP {
		if ip := net.ParseIP(c.BindAddr); ip == nil || !ip.IsLoopback() {
			r.fail("local admission and CTR_AUTH_HTTP require a literal loopback BIND_ADDR")
		}
		if ip := net.ParseIP(c.PublicIP); ip == nil || !ip.IsLoopback() {
			r.fail("local admission and CTR_AUTH_HTTP require a loopback NEXTENDO_HOST")
		}
		if c.ProxyProtocol {
			r.fail("local admission and CTR_AUTH_HTTP forbid NEXTENDO_PROXY_PROTOCOL")
		}
	}
	if admission == "local" && (c.RelayLow != 0 || c.RelayHigh != 0 || c.RelayPIDs != "") {
		r.fail("local admission forbids the central UDP relay")
	}
	if admission == "local" && !c.RedactLogs {
		r.fail("local admission requires NEXTENDO_REDACT_LOGS")
	}
	if needsShadNet && c.Shadnet.Issuer != "" && c.Shadnet.ClientID != "" && c.Shadnet.NPTitleID != "" &&
		len(c.Shadnet.Scopes) != 0 && c.JWKSURL != "" {
		if err := shadnet.Check(c.Shadnet, c.JWKSURL); err != nil {
			r.fail("SHADNET_*: %v", err)
		}
	}
	if (c.RelayLow == 0) != (c.RelayHigh == 0) || c.RelayLow > c.RelayHigh {
		r.fail("CTR_RELAY_PORT_LOW and CTR_RELAY_PORT_HIGH must both be 0 or form a range")
	}
	for _, name := range check {
		if !r.known[name] && !testOnlySettings[name] {
			r.fail("%s is not a setting of this server (see example.env)", name)
		}
	}
	return c, errors.Join(r.errs...)
}

type envReader struct {
	lookup func(string) string
	known  map[string]bool
	errs   []error
}

func (r *envReader) fail(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

func (r *envReader) value(name string) string {
	r.known[name] = true
	return r.lookup(name)
}

// Game language codes, including the two regional variants (CUSA13795 01.21).
var newsLanguages = []string{
	"ar", "bg", "zh", "cs", "da", "nl", "en", "fi", "fr", "de", "el", "he", "hi", "hu",
	"it", "ja", "ko", "no", "pl", "pt", "ru", "es", "mx", "sv", "ta", "tr", "pt-BR", "es-MX",
}

func (r *envReader) news() News {
	c := News{Fallback: r.newsMessage("CTR_NEWS"), Languages: map[string]NewsMessage{}}
	for _, language := range newsLanguages {
		name := "CTR_NEWS_" + strings.ReplaceAll(strings.ToUpper(language), "-", "_")
		if msg := r.newsMessage(name); len(msg.Content) != 0 {
			c.Languages[language] = msg
		}
	}
	return c
}

func (r *envReader) newsMessage(name string) NewsMessage {
	msg, err := newNewsMessage(r.value(name))
	if err != nil {
		r.fail("%s: %v", name, err)
	}
	return msg
}

func (r *envReader) text(name, def string) string {
	if v := r.value(name); v != "" {
		return v
	}
	return def
}

func (r *envReader) required(name string, needed bool) string {
	v := r.value(name)
	if v == "" && needed {
		r.fail("%s is required", name)
	}
	return v
}

// flag accepts 0, false, 1 and true; empty keeps the default.
func (r *envReader) flag(name string, def bool) bool {
	v, err := ParseFlag(r.value(name), def)
	if err != nil {
		r.fail("%s: %v", name, err)
	}
	return v
}

func ParseFlag(v string, def bool) (bool, error) {
	switch v {
	case "":
		return def, nil
	case "0", "false":
		return false, nil
	case "1", "true":
		return true, nil
	}
	return def, fmt.Errorf("expected 0, false, 1 or true")
}

func (r *envReader) integer(name string, def, minimum, maximum int) int {
	v := r.value(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minimum || n > maximum {
		r.fail("%s: expected an integer from %d to %d", name, minimum, maximum)
		return def
	}
	return n
}

func (r *envReader) port(name string, def int) int { return r.integer(name, def, 1, 65535) }

// ports reads a comma-separated list of ports and returns it normalized.
func (r *envReader) ports(name, def string) string {
	v := r.value(name)
	if v == "" {
		return def
	}
	var out []string
	for _, field := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n < 1 || n > 65535 {
			r.fail("%s: expected comma-separated ports", name)
			return def
		}
		out = append(out, strconv.Itoa(n))
	}
	return strings.Join(out, ",")
}

func (r *envReader) balance(name string) int64 {
	v := r.value(name)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		r.fail("%s: expected a non-negative integer", name)
		return 0
	}
	return n
}

// ipv4 is the address announced to players, so it must be literal IPv4.
func (r *envReader) ipv4(name, def string) string {
	v := r.text(name, def)
	if ip := net.ParseIP(v); ip == nil || ip.To4() == nil {
		r.fail("%s: expected an IPv4 address", name)
		return def
	}
	return v
}

// ip is an optional literal listener address.
func (r *envReader) ip(name string) string {
	v := r.value(name)
	if v != "" && net.ParseIP(v) == nil {
		r.fail("%s: expected an IP address", name)
		return ""
	}
	return v
}

// pids is empty (nobody), * (everyone) or comma-separated account IDs.
func (r *envReader) pids(name string) string {
	v := r.value(name)
	if v == "" || v == "*" {
		return v
	}
	for _, field := range strings.Split(v, ",") {
		if _, err := strconv.ParseUint(strings.TrimSpace(field), 10, 64); err != nil {
			r.fail("%s: expected *, or comma-separated account IDs", name)
			return ""
		}
	}
	return v
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadEnvFile sets the variables of an env file (parseEnvFile). A variable
// already in the process environment keeps its value.
func LoadEnvFile(path string) error {
	vars, err := ParseEnvFile(path)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if _, set := os.LookupEnv(v[0]); !set {
			if err := os.Setenv(v[0], v[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ParseEnvFile reads NAME=value lines in order, as `docker --env-file` does:
// leading blanks, blank lines and lines starting with # are skipped, and the
// value is everything after the first =, kept exactly (no quote processing).
// A leading UTF-8 byte order mark and CRLF line ends are accepted.
func ParseEnvFile(path string) ([][2]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var vars [][2]string
	seen := map[string]bool{}
	for i, line := range strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
		line = strings.TrimLeftFunc(strings.TrimSuffix(line, "\r"), unicode.IsSpace)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || !envName.MatchString(name) {
			return nil, fmt.Errorf("%s:%d: expected NAME=value", path, i+1)
		}
		if seen[canonicalName(name)] {
			return nil, fmt.Errorf("%s:%d: %s is set twice", path, i+1, name)
		}
		seen[canonicalName(name)] = true
		vars = append(vars, [2]string{name, value})
	}
	return vars, nil
}
