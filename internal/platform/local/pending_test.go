package local

import (
	"encoding/json"
	"testing"
)

func TestPendingLoopbackDescriptorHasNoInventedSource(t *testing.T) {
	var transport VirtualTransport
	if err := json.Unmarshal([]byte(`{"mode":"loopback-v1","address":"127.64.1.2","port":12000,"game_pending":true}`), &transport); err != nil {
		t.Fatal(err)
	}
	game, canonical, err := transport.Endpoints()
	if err != nil || game.IsValid() || canonical.String() != "127.64.1.2:12000" {
		t.Fatal("pending descriptor invented a source or changed canonical endpoint")
	}
	for _, raw := range []string{
		`{"mode":"loopback-v1","address":"127.64.1.2","port":12000,"game_pending":true,"game_address":""}`,
		`{"mode":"loopback-v1","address":"127.64.1.2","port":12000,"game_pending":true,"game_port":0}`,
		`{"mode":"loopback-v1","address":"127.64.1.2","port":12000,"game_pending":false}`,
		`{"mode":"loopback-v1","address":"127.64.1.2","port":12000,"game_pending":null}`,
		`{"mode":"loopback-v1","address":"127.0.1.2","port":12000,"game_pending":true}`,
		`{"mode":"loopback-v1","address":"127.64.1.2","port":12000,"game_pending":true,"unexpected":1}`,
		`{"mode":"virtual-v1","address":"198.19.1.2","port":12000,"game_pending":true}`,
	} {
		var invalid VirtualTransport
		if err := json.Unmarshal([]byte(raw), &invalid); err != nil {
			continue
		}
		if _, _, err := invalid.Endpoints(); err == nil {
			t.Fatal("invalid pending descriptor accepted")
		}
	}
}
