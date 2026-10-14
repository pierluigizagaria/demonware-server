package main

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

	"github.com/NextendoNetwork/nextendo-demonware/internal/platform/shadnet"
)

// Settings come only from environment variables, as in the other Nextendo
// servers; example.env documents each one. `server -env <file>` loads one
// instance's KEY=VALUE file first. Everything is read and validated once,
// here, before anything starts: a malformed or missing value, or an unknown
// variable with one of this server's prefixes, stops the server with every
// problem listed instead of falling back to a default. Defaults are fresh values, independent of any running instance.

var settingPrefixes = []string{"CTR_", "D3_", "DASH_", "NEXTENDO_", "SHADNET_"}

// Read by the integration test only, never by the server.
var testOnlySettings = map[string]bool{"SHADNET_INTEROP_FIXTURE": true}

type serverConfig struct {
	publicIP, bindAddr                 string
	authPort, natPort, dashPort        int
	lobbyPorts                         string
	proxyProtocol                      bool
	certFile, keyFile                  string
	dashToken, sniHost                 string
	pubfilesConfig, pubfilesDir, dumps string
	verbose                            bool
	riftData                           string
	shadnet                            shadnet.Policy
	admission                          string
	jwksURL                            string
	signingKey, dataDir                string
	unlockAll, migrate, redactLogs     bool
	startBalance                       int64
	relayLow, relayHigh                int
	relayPIDs                          string
	news                               ctrNewsConfig
}

// readConfig loads envFile, if any, and reads the configuration. serving
// requires what the server needs beyond `server pubfiles`.
func readConfig(envFile string, serving bool) (serverConfig, error) {
	return readConfigEnvironment(envFile, serving, os.Environ())
}

// Read the file as an instance overlay. Existing process values (including an
// explicitly empty value) win, without installing file values into the process.
func readConfigEnvironment(envFile string, serving bool, environ []string) (serverConfig, error) {
	values := map[string]string{}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if ok {
			values[canonicalName(name)] = value
		}
	}
	names := append([]string(nil), environ...)
	if envFile != "" {
		vars, err := parseEnvFile(envFile)
		if err != nil {
			return serverConfig{}, err
		}
		for _, v := range vars {
			name := canonicalName(v[0])
			if _, exists := values[name]; exists {
				continue
			}
			if strings.IndexByte(v[1], 0) >= 0 {
				return serverConfig{}, fmt.Errorf("%s: environment value contains NUL", v[0])
			}
			values[name] = v[1]
			names = append(names, name+"="+v[1])
		}
	}
	return loadConfig(func(name string) string { return values[canonicalName(name)] }, settingNames(names), serving)
}

// settingNames returns the canonical names in environ, without repeats, that
// carry one of this server's prefixes; the env file's variables are among them.
func settingNames(environ []string) []string {
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

// loadConfig reads every setting through lookup. Each name in check, the
// environment's variables with a server prefix, must be a setting.
func loadConfig(lookup func(string) string, check []string, serving bool) (serverConfig, error) {
	r := &envReader{lookup: lookup, known: map[string]bool{}}
	admission := r.text("CTR_ADMISSION", "shadnet")
	if admission != "shadnet" && admission != "guest" {
		r.fail("CTR_ADMISSION must be shadnet or guest")
	}
	needsShadNet := serving && admission != "guest"
	c := serverConfig{
		admission:      admission,
		publicIP:       r.ipv4("NEXTENDO_HOST", "127.0.0.1"),
		bindAddr:       r.ip("BIND_ADDR"),
		authPort:       r.port("AUTH_PORT", 8460),
		lobbyPorts:     r.ports("LOBBY_PORTS", "3074,3075,3076,3077,3078,3079,3080"),
		natPort:        r.port("NAT_PORT", 3074),
		proxyProtocol:  r.flag("NEXTENDO_PROXY_PROTOCOL", false),
		certFile:       r.text("CERT_FILE", "cert.pem"),
		keyFile:        r.text("KEY_FILE", "key.pem"),
		dashPort:       r.port("DASH_PORT", 8093),
		dashToken:      r.text("DASH_TOKEN", ""),
		sniHost:        r.text("NEXTENDO_SNI_HOST", ""),
		pubfilesConfig: r.text("D3_PUBFILES_CONFIG", "pubfiles.json"),
		pubfilesDir:    r.text("D3_PUBFILES", "pubfiles"),
		dumps:          r.text("D3_DUMPS", "off"),
		verbose:        r.flag("D3_VERBOSE", false),
		riftData:       r.text("D3_RIFTDATA", "riftdata"),
		shadnet: shadnet.Policy{
			Issuer:    r.required("SHADNET_ISSUER", needsShadNet),
			ClientID:  r.required("SHADNET_CLIENT_ID", needsShadNet),
			NPTitleID: r.required("SHADNET_TITLE_ID", needsShadNet),
			Scopes:    strings.Fields(r.required("SHADNET_SCOPES", needsShadNet)),
		},
		jwksURL:      r.required("SHADNET_JWKS_URL", needsShadNet),
		signingKey:   r.required("CTR_AUTH_SIGNING_KEY", serving),
		dataDir:      r.text("DATA_DIR", "data"),
		unlockAll:    r.flag("CTR_UNLOCK_ALL", false),
		startBalance: r.balance("CTR_START_BALANCE"),
		migrate:      r.flag("CTR_MIGRATE", false),
		relayLow:     r.integer("CTR_RELAY_PORT_LOW", 0, 0, 65535),
		relayHigh:    r.integer("CTR_RELAY_PORT_HIGH", 0, 0, 65535),
		relayPIDs:    r.pids("CTR_RELAY_PIDS"),
		redactLogs:   r.flag("NEXTENDO_REDACT_LOGS", true),
		news:         r.news(),
	}
	if admission == "guest" {
		if !c.unlockAll {
			r.fail("CTR_ADMISSION=guest requires CTR_UNLOCK_ALL=1")
		}
		if c.shadnet.Issuer != "" || c.shadnet.ClientID != "" || c.shadnet.NPTitleID != "" || len(c.shadnet.Scopes) != 0 || c.jwksURL != "" {
			r.fail("SHADNET_* must be empty for guest admission")
		}
	}
	if needsShadNet && c.shadnet.Issuer != "" && c.shadnet.ClientID != "" && c.shadnet.NPTitleID != "" &&
		len(c.shadnet.Scopes) != 0 && c.jwksURL != "" {
		if err := shadnet.Check(c.shadnet, c.jwksURL); err != nil {
			r.fail("SHADNET_*: %v", err)
		}
	}
	if (c.relayLow == 0) != (c.relayHigh == 0) || c.relayLow > c.relayHigh {
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

func (r *envReader) news() ctrNewsConfig {
	c := ctrNewsConfig{fallback: r.newsMessage("CTR_NEWS"), languages: map[string]ctrNewsMessage{}}
	for _, language := range newsLanguages {
		name := "CTR_NEWS_" + strings.ReplaceAll(strings.ToUpper(language), "-", "_")
		if msg := r.newsMessage(name); len(msg.content) != 0 {
			c.languages[language] = msg
		}
	}
	return c
}

func (r *envReader) newsMessage(name string) ctrNewsMessage {
	msg, err := newCTRNewsMessage(r.value(name))
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
	v, err := parseFlag(r.value(name), def)
	if err != nil {
		r.fail("%s: %v", name, err)
	}
	return v
}

func parseFlag(v string, def bool) (bool, error) {
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

// loadEnvFile sets the variables of an env file (parseEnvFile). A variable
// already in the process environment keeps its value.
func loadEnvFile(path string) error {
	vars, err := parseEnvFile(path)
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

// parseEnvFile reads NAME=value lines in order, as `docker --env-file` does:
// leading blanks, blank lines and lines starting with # are skipped, and the
// value is everything after the first =, kept exactly (no quote processing).
// A leading UTF-8 byte order mark and CRLF line ends are accepted.
func parseEnvFile(path string) ([][2]string, error) {
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
