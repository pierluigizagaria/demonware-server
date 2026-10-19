package config

import (
	"strings"
	"testing"
)

func TestSteamAdmissionConfigIsIndependent(t *testing.T) {
	env := map[string]string{"CTR_ADMISSION": "steam", "CTR_STEAM_REALM": "normal",
		"CTR_STEAM_HELPER": "osw-auth-helper.exe"}
	if c, err := Load(lookupFrom(env), nil, true); err != nil || c.Admission != "steam" || c.UnlockAll {
		t.Fatal("independent Steam config refused", err)
	}
	for _, key := range []string{"CTR_STEAM_HELPER", "CTR_STEAM_REALM"} {
		old := env[key]
		delete(env, key)
		if _, err := Load(lookupFrom(env), nil, true); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatal("missing required setting accepted")
		}
		env[key] = old
	}
	env["SHADNET_ISSUER"] = "unrelated"
	if _, err := Load(lookupFrom(env), nil, true); err == nil {
		t.Fatal("mixed providers accepted")
	}
	delete(env, "SHADNET_ISSUER")
	env["CTR_STEAM_REALM"] = "bad/realm"
	if _, err := Load(lookupFrom(env), nil, true); err == nil {
		t.Fatal("invalid trust realm accepted")
	}
}
