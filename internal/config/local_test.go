package config

import "testing"

func TestLocalModeValidation(t *testing.T) {
	base := map[string]string{"CTR_ADMISSION": "local", "CTR_COMPANION": "stdio", "CTR_MATCHMAKING": "steam", "CTR_AUTH_HTTP": "1", "BIND_ADDR": "127.0.0.5", "NEXTENDO_HOST": "127.0.0.5", "CTR_AUTH_SIGNING_KEY": "fixture.pem"}
	if c, err := Load(lookupFrom(base), nil, true); err != nil || !c.AuthHTTP || c.Matchmaking != "steam" {
		t.Fatal(err)
	}
	for _, bad := range []map[string]string{{"BIND_ADDR": ""}, {"BIND_ADDR": "0.0.0.0"}, {"NEXTENDO_HOST": "192.168.1.1"}, {"NEXTENDO_PROXY_PROTOCOL": "1"}, {"CTR_ADMISSION": "guest", "CTR_UNLOCK_ALL": "1"}, {"CTR_MATCHMAKING": "other"}, {"CTR_RELAY_PORT_LOW": "4000", "CTR_RELAY_PORT_HIGH": "4010"}, {"SHADNET_ISSUER": "unrelated"}} {
		e := map[string]string{}
		for k, v := range base {
			e[k] = v
		}
		for k, v := range bad {
			e[k] = v
		}
		if _, err := Load(lookupFrom(e), nil, true); err == nil {
			t.Fatalf("invalid mode accepted: %v", bad)
		}
	}
	base["CTR_MATCHMAKING"] = "central"
	if _, err := Load(lookupFrom(base), nil, true); err != nil {
		t.Fatal("P1 central local mode refused", err)
	}
}
