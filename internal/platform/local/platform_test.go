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
	other = o
	other.Realm = "unlock-all"
	q, _ = New(other)
	if p.TrustID() == q.TrustID() {
		t.Fatal("economy realm not bound")
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
