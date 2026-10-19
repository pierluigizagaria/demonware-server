package local

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func ownerFixture() Owner {
	return Owner{SteamID: "76561198000000001", OnlineID: "Giocàtore", Token: "osw1.76561198000000001.abababababababababababababababababababababababababababababababab." + base64.RawURLEncoding.EncodeToString([]byte("Giocàtore")), Title: "5760", Build: "01.21", Realm: "normal", Backend: "lab"}
}

func envelope(token string) []byte {
	t, _ := json.Marshal(map[string]string{"id_token": token})
	e, _ := json.Marshal(map[string]string{"token": string(t)})
	b, _ := json.Marshal(map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "1", "extra_data": string(e)})
	return b
}

func TestOwnerAdmissionUsesInheritedCredential(t *testing.T) {
	o := ownerFixture()
	p, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Authenticate(envelope(o.Token))
	if err != nil || id.PID != 76561198000000001 || id.Username != o.OnlineID || id.Kind != "local" {
		t.Fatal("owner refused")
	}
	for _, token := range []string{"", strings.Replace(o.Token, "ababab", "123456", 1), strings.Replace(o.Token, o.SteamID, "76561198000000002", 1)} {
		if _, err := p.Authenticate(envelope(token)); err == nil {
			t.Fatal("foreign or stale credential admitted")
		}
	}
	// Repeated owner connections are valid; individual publisher tickets remain fresh.
	if _, err := p.Authenticate(envelope(o.Token)); err != nil {
		t.Fatal(err)
	}
	other := o
	other.Backend = "steam"
	q, _ := New(other)
	if p.TrustID() == q.TrustID() {
		t.Fatal("lab shares Steam database namespace")
	}
	// Realm and owner select sessions, not the database: one file per backend.
	other = o
	other.Realm = "unlock-all"
	q, _ = New(other)
	if p.TrustID() != q.TrustID() || p.TrustID() != "ctrnf:local:v2:lab" {
		t.Fatal("realm changed the database namespace")
	}
}

func TestLegacyTrustIDIsExactAndSameBackend(t *testing.T) {
	p, err := New(ownerFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, trust := range []string{
		"ctrnf:local:480:5760:v1:lab:normal:76561198000000001",
		"ctrnf:local:876543:5760:v1:lab:other-realm_2:76561198000000002",
	} {
		if !p.LegacyTrustID(trust) {
			t.Fatalf("legacy binding refused: %s", trust)
		}
	}
	for _, trust := range []string{
		"", p.TrustID(), "ctrnf:local:480:5760:v1:steam:normal:76561198000000001",
		"ctrnf:local:0:5760:v1:lab:normal:1", "ctrnf:local:0480:5760:v1:lab:normal:1",
		"ctrnf:local:4294967296:5760:v1:lab:normal:1", "ctrnf:local:480:5761:v1:lab:normal:1",
		"ctrnf:local:480:5760:v2:lab:normal:1", "ctrnf:local:480:5760:v1:lab:bad/realm:1",
		"ctrnf:local:480:5760:v1:lab:normal:0", "ctrnf:local:480:5760:v1:lab:normal:01",
		"ctrnf:local:480:5760:v1:lab:normal:1:extra", "ctrnf:steam:480:5760:v1:normal",
		"ctrnf:anonymous-guests:v1",
	} {
		if p.LegacyTrustID(trust) {
			t.Fatalf("invalid legacy binding accepted: %q", trust)
		}
	}
}

func TestInvalidOwnerRefused(t *testing.T) {
	for _, change := range []func(*Owner){func(o *Owner) { o.SteamID = "1" }, func(o *Owner) { o.OnlineID = "other" }, func(o *Owner) { o.Realm = "bad/realm" }, func(o *Owner) { o.Backend = "fake" }, func(o *Owner) { o.Title = "" }} {
		o := ownerFixture()
		change(&o)
		if _, err := New(o); err == nil {
			t.Fatal("invalid owner admitted")
		}
	}
}

func TestVirtualOwnerTransportIsExplicitAndBounded(t *testing.T) {
	valid := VirtualTransport{Mode: "virtual-v1", Address: "198.19.255.255", Port: 65535, GameAddress: "127.0.0.11", GamePort: 3074}
	owner := ownerFixture()
	owner.Transport = &valid
	if _, err := New(owner); err != nil {
		t.Fatal("valid virtual owner refused")
	}
	for _, change := range []func(*VirtualTransport){
		func(v *VirtualTransport) { v.Mode = "unknown" },
		func(v *VirtualTransport) { v.Address = "invalid" },
		func(v *VirtualTransport) { v.Address = "::ffff:198.18.1.2" },
		func(v *VirtualTransport) { v.Port = 0 },
		func(v *VirtualTransport) { v.GameAddress = "198.18.1.2" },
		func(v *VirtualTransport) { v.GameAddress = "::1" },
		func(v *VirtualTransport) { v.GamePort = 0 },
	} {
		transport := valid
		change(&transport)
		owner.Transport = &transport
		if _, err := New(owner); err == nil {
			t.Fatal("invalid virtual transport admitted")
		}
	}
}

func TestAdmissionRefusalCategoriesContainNoInput(t *testing.T) {
	p, _ := New(ownerFixture())
	valid := string(envelope(ownerFixture().Token))
	for _, test := range []struct {
		body string
		want AdmissionError
	}{
		{`{"auth_task":"45"}`, UnsupportedTask},
		{`{"auth_task":"private-arbitrary-input"}`, UnsupportedTask},
		{`{"auth_task":"46"}`, MalformedEnvelope},
		{`{"auth_task":"45","auth_task":"46"}`, MalformedEnvelope},
		{`{"auth_task":null}`, MalformedEnvelope},
		{strings.Replace(valid, `"title_id":"5760"`, `"title_id":"5775"`, 1), MalformedEnvelope},
		{string(envelope("private-arbitrary-input")), OwnerCapabilityMismatch},
	} {
		id, err := p.Authenticate([]byte(test.body))
		if err != test.want || id.PID != 0 || err.Error() != "local owner admission refused" {
			t.Fatal("refusal category or redaction changed")
		}
	}
}
