package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/config"
)

func setStartBalance(t *testing.T, n int64) {
	t.Helper()
	old := testStartBalance
	testStartBalance = n
	t.Cleanup(func() { testStartBalance = old })
}

func setRelayPIDs(t *testing.T, list string) {
	t.Helper()
	old := testRelay.Pids
	testRelay.Pids = list
	t.Cleanup(func() { testRelay.Pids = old })
}

func lookupFrom(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func serverEnv() map[string]string {
	return map[string]string{
		"SHADNET_ISSUER":       "https://identity.example.net",
		"SHADNET_CLIENT_ID":    "example-application",
		"SHADNET_TITLE_ID":     "CUSA13795",
		"SHADNET_SCOPES":       "openid openid:duid",
		"SHADNET_JWKS_URL":     "https://identity.example.net/.well-known/jwks.json",
		"CTR_AUTH_SIGNING_KEY": "keys/ctr-signing.pem",
	}
}

func TestParseFlag(t *testing.T) {
	for in, want := range map[string]bool{"0": false, "false": false, "1": true, "true": true} {
		if got, err := config.ParseFlag(in, !want); err != nil || got != want {
			t.Fatalf("%q = %v, %v", in, got, err)
		}
	}
	for _, def := range []bool{false, true} {
		if got, err := config.ParseFlag("", def); err != nil || got != def {
			t.Fatal("empty value did not keep the default")
		}
	}
	for _, in := range []string{"unlock-all", "all", "yes", "2", "TRUE", "False", " true", "false ", "\n"} {
		if _, err := config.ParseFlag(in, false); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

func TestConfigDefaultsNeedNoEnvironment(t *testing.T) {
	c, err := config.LoadConfig(lookupFrom(nil), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicIP != testConfig.PublicIP || c.AuthPort != testConfig.AuthPort || c.LobbyPorts != testConfig.LobbyPorts || c.DataDir != testConfig.DataDir ||
		c.DashPort != testConfig.DashPort || !c.RedactLogs || c.UnlockAll || c.StartBalance != 0 || c.RelayLow != 0 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestConfigDefaultsDoNotReadInstalledSettings(t *testing.T) {
	want, err := config.LoadConfig(lookupFrom(nil), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	changed := want
	changed.PublicIP = "127.0.0.2"
	changed.AuthPort, changed.NatPort, changed.DashPort = 30001, 30002, 30003
	changed.LobbyPorts = "30004"
	changed.CertFile, changed.KeyFile = "other-cert", "other-key"
	changed.DataDir, changed.PubfilesConfig, changed.PubfilesDir, changed.RiftData = "other-data", "other-config", "other-files", "other-rifts"
	changed.Dumps, changed.Verbose, changed.ProxyProtocol, changed.Migrate, changed.RedactLogs = "other-dumps", true, true, true, false
	changed.DashToken, changed.SniHost = "synthetic-dashboard-value", "synthetic-host"
	testApplyConfig(changed)
	t.Cleanup(func() { testApplyConfig(want) })
	got, err := config.LoadConfig(lookupFrom(nil), nil, false)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("configuration defaults inherited prior instance settings")
	}
}

func TestConfigReadsServerSettings(t *testing.T) {
	env := serverEnv()
	env["BIND_ADDR"], env["AUTH_PORT"], env["LOBBY_PORTS"] = "127.0.0.2", "31443", "3074, 3075"
	env["CTR_UNLOCK_ALL"], env["CTR_START_BALANCE"], env["NEXTENDO_REDACT_LOGS"] = "true", "500", "0"
	env["CTR_RELAY_PORT_LOW"], env["CTR_RELAY_PORT_HIGH"], env["CTR_RELAY_PIDS"] = "3100", "3139", "7, 9"
	c, err := config.LoadConfig(lookupFrom(env), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.BindAddr != "127.0.0.2" || c.AuthPort != 31443 || c.LobbyPorts != "3074,3075" || !c.UnlockAll ||
		c.StartBalance != 500 || c.RedactLogs || c.RelayLow != 3100 || c.RelayHigh != 3139 || c.RelayPIDs != "7, 9" {
		t.Fatalf("settings not read: %+v", c)
	}
	if !reflect.DeepEqual(c.Shadnet.Scopes, []string{"openid", "openid:duid"}) || c.Shadnet.NPTitleID != "CUSA13795" ||
		c.JwksURL != env["SHADNET_JWKS_URL"] || c.SigningKey != "keys/ctr-signing.pem" {
		t.Fatalf("shadNet trust not read: %+v", c.Shadnet)
	}
}

func TestConfigRefusesWithEveryProblemListed(t *testing.T) {
	_, err := config.LoadConfig(lookupFrom(nil), nil, true)
	for _, name := range []string{"SHADNET_ISSUER", "SHADNET_CLIENT_ID", "SHADNET_TITLE_ID", "SHADNET_SCOPES", "SHADNET_JWKS_URL", "CTR_AUTH_SIGNING_KEY"} {
		if err == nil || !strings.Contains(err.Error(), name+" is required") {
			t.Fatalf("missing %s not reported: %v", name, err)
		}
	}

	bad := map[string]string{
		"NEXTENDO_HOST": "server.example", "BIND_ADDR": "localhost", "AUTH_PORT": "0", "NAT_PORT": "65536",
		"LOBBY_PORTS": "3074,,3075", "DASH_PORT": "x", "CTR_UNLOCK_ALL": "yes", "CTR_START_BALANCE": "-1",
		"D3_VERBOSE": "2", "CTR_RELAY_PIDS": "7,x", "CTR_RELAY_PORT_LOW": "3100",
	}
	env := serverEnv()
	for name, value := range bad {
		env[name] = value
	}
	_, err = config.LoadConfig(lookupFrom(env), nil, true)
	for name := range bad {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("invalid %s not reported: %v", name, err)
		}
	}

	// Invalid shadNet trust is reported before any key is fetched.
	env = serverEnv()
	env["SHADNET_ISSUER"], env["SHADNET_JWKS_URL"] = "http://identity.example.net", "jwks.json"
	_, err = config.LoadConfig(lookupFrom(env), nil, true)
	if err == nil || !strings.Contains(err.Error(), "SHADNET_*: invalid shadNet trust policy") || !strings.Contains(err.Error(), "JWKS requires") {
		t.Fatalf("invalid shadNet trust not reported: %v", err)
	}
	if _, err := config.LoadConfig(lookupFrom(serverEnv()), nil, true); err != nil {
		t.Fatal(err)
	}
}

func TestConfigRefusesUnknownSettings(t *testing.T) {
	_, err := config.LoadConfig(lookupFrom(serverEnv()), []string{"CTR_UNLOCKALL", "SHADNET_INTEROP_FIXTURE", "CTR_UNLOCK_ALL"}, true)
	if err == nil || !strings.Contains(err.Error(), "CTR_UNLOCKALL is not a setting") ||
		strings.Contains(err.Error(), "INTEROP") || strings.Contains(err.Error(), "CTR_UNLOCK_ALL ") {
		t.Fatalf("unknown settings misreported: %v", err)
	}
	// Upstream Nintendo settings no longer exist here.
	if _, err := config.LoadConfig(lookupFrom(serverEnv()), []string{"NEXTENDO_ACCOUNT_URL", "CTR_AUTH_CONFIG"}, true); err == nil ||
		!strings.Contains(err.Error(), "NEXTENDO_ACCOUNT_URL") || !strings.Contains(err.Error(), "CTR_AUTH_CONFIG") {
		t.Fatalf("retired settings accepted: %v", err)
	}
}

// example.env must be a valid env file that names every setting.
func TestExampleEnvDocumentsEverySetting(t *testing.T) {
	vars, err := config.ParseEnvFile(filepath.Join("..", "..", "example.env"))
	if err != nil {
		t.Fatal(err)
	}
	env, names := map[string]string{}, []string{}
	for _, v := range vars {
		env[v[0]] = v[1]
		names = append(names, v[0])
	}
	asked := map[string]bool{}
	lookup := func(name string) string {
		asked[name] = true
		return env[name]
	}
	if _, err := config.LoadConfig(lookup, names, false); err != nil {
		t.Fatal(err)
	}
	for name := range asked {
		if _, ok := env[name]; !ok {
			t.Errorf("%s is missing from example.env", name)
		}
	}
}

func TestLoadEnvFile(t *testing.T) {
	names := []string{"NEXTENDO_TEST_A", "NEXTENDO_TEST_B", "NEXTENDO_TEST_C"}
	for _, name := range names {
		t.Cleanup(func() { os.Unsetenv(name) })
	}
	t.Setenv("NEXTENDO_TEST_C", "from environment")
	path := filepath.Join(t.TempDir(), "instance.env")
	// A byte order mark, CRLF, indented lines and comments, as Windows editors write them.
	content := "\ufeff# comment\r\n\r\n  NEXTENDO_TEST_A=openid openid:duid \r\n\t# indented\nNEXTENDO_TEST_B=\"quoted\"=x\nNEXTENDO_TEST_C=from file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	vars, err := config.ParseEnvFile(path)
	want := [][2]string{{names[0], "openid openid:duid "}, {names[1], `"quoted"=x`}, {names[2], "from file"}}
	if err != nil || !reflect.DeepEqual(vars, want) {
		t.Fatalf("parsed %q, %v", vars, err)
	}
	if err := config.LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(names[0]) != "openid openid:duid " || os.Getenv(names[1]) != `"quoted"=x` || os.Getenv(names[2]) != "from environment" {
		t.Fatal("env file values or precedence wrong")
	}
	for _, bad := range []string{"NEXTENDO_TEST_A=1\nNEXTENDO_TEST_A=2\n", "NEXTENDO_TEST_A =1\n", "export NEXTENDO_TEST_A=1\n", "just text\n", "1BAD=x\n"} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.ParseEnvFile(path); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if err := config.LoadEnvFile(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestSettingNamesFollowThePlatformCase(t *testing.T) {
	got := config.SettingNames([]string{"PATH=x", "=C:=C:\\", "CTR_UNLOCK_ALL=1", "CTR_Unlock_All=1", "ctr_typo=1", "SHADNET_ISSUER=a=b"})
	want := []string{"CTR_UNLOCK_ALL", "CTR_Unlock_All", "SHADNET_ISSUER"}
	if runtime.GOOS == "windows" {
		// Names are case-insensitive there: CTR_Unlock_All is CTR_UNLOCK_ALL,
		// and ctr_typo is a CTR_ variable the check must see.
		want = []string{"CTR_UNLOCK_ALL", "CTR_TYPO", "SHADNET_ISSUER"}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestConfigFilesAreIndependentInOneProcess(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a.env"), filepath.Join(t.TempDir(), "b.env")
	if err := os.WriteFile(a, []byte("CTR_NEWS=first\nCTR_START_BALANCE=17\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("CTR_NEWS=second\nCTR_START_BALANCE=18\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first, e1 := config.ReadConfigEnvironment(a, false, nil)
	second, e2 := config.ReadConfigEnvironment(b, false, nil)
	if e1 != nil || e2 != nil || first.StartBalance != 17 || second.StartBalance != 18 || bytes.Equal(first.News.Fallback.Content, second.News.Fallback.Content) {
		t.Fatal("instance file values crossed")
	}
	overridden, err := config.ReadConfigEnvironment(a, false, []string{"CTR_NEWS=", "CTR_START_BALANCE=19"})
	if err != nil || overridden.StartBalance != 19 || len(overridden.News.Fallback.Content) != 0 {
		t.Fatal("process environment precedence changed")
	}
}
