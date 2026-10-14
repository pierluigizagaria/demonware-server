package main

import (
	"reflect"
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
	old := testRelay.PIDs
	testRelay.PIDs = list
	t.Cleanup(func() { testRelay.PIDs = old })
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

func TestConfigDefaultsDoNotReadInstalledSettings(t *testing.T) {
	want, err := config.Load(lookupFrom(nil), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	changed := want
	changed.PublicIP = "127.0.0.2"
	changed.AuthPort, changed.NATPort, changed.DashPort = 30001, 30002, 30003
	changed.LobbyPorts = "30004"
	changed.CertFile, changed.KeyFile = "other-cert", "other-key"
	changed.DataDir, changed.PubfilesConfig, changed.PubfilesDir, changed.RiftData = "other-data", "other-config", "other-files", "other-rifts"
	changed.Dumps, changed.Verbose, changed.ProxyProtocol, changed.Migrate, changed.RedactLogs = "other-dumps", true, true, true, false
	changed.DashToken, changed.SNIHost = "synthetic-dashboard-value", "synthetic-host"
	testApplyConfig(changed)
	t.Cleanup(func() { testApplyConfig(want) })
	got, err := config.Load(lookupFrom(nil), nil, false)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("configuration defaults inherited prior instance settings")
	}
}
