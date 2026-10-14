//go:build integration

package main

import (
	bytes "bytes"
	crypto "crypto"
	rand "crypto/rand"
	rsa "crypto/rsa"
	sha256 "crypto/sha256"
	x509 "crypto/x509"
	base64 "encoding/base64"
	json "encoding/json"
	pem "encoding/pem"
	authpkg "github.com/pierluigizagaria/demonware-server/internal/auth"
	ps4pkg "github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	shadnetpkg "github.com/pierluigizagaria/demonware-server/internal/platform/shadnet"
	httptest "net/http/httptest"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
	time "time"
)

// An external native shadNet fixture supplies disposable credentials. The
// normal suite has no service dependency; this check fails without its input.
func TestShadNetPublisherInterop(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("SHADNET_INTEROP_FIXTURE"))
	if err != nil {
		t.Fatal("native fixture unavailable")
	}
	var f struct {
		Config string `json:"config"`
		Users  []struct {
			ID    uint64 `json:"id"`
			Name  string `json:"name"`
			Token string `json:"token"`
		} `json:"users"`
	}
	if json.Unmarshal(raw, &f) != nil || len(f.Users) != 2 || f.Users[0].ID == f.Users[1].ID {
		t.Fatal("invalid fixture metadata")
	}
	// The fixture names a JSON trust file with the SHADNET_* values.
	var trust struct {
		shadnetpkg.Policy
		JWKSURL string `json:"jwks_url"`
	}
	raw, err = os.ReadFile(f.Config)
	if err != nil || json.Unmarshal(raw, &trust) != nil {
		t.Fatal("invalid fixture trust file")
	}
	p, _, err := ps4pkg.LoadPSNPlatform(trust.Policy, trust.JWKSURL)
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "signer.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := authpkg.LoadCTRAuthSigner(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	handler := testAuthHandler(p, signer, "")
	for _, user := range f.Users {
		parts := strings.Split(user.Token, ".")
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		var claims struct {
			Iat int64 `json:"iat"`
			Exp int64 `json:"exp"`
		}
		if len(parts) != 3 || err != nil || json.Unmarshal(payload, &claims) != nil || claims.Exp-claims.Iat != 120 {
			t.Fatal("unexpected native token")
		}
		for _, tc := range []struct {
			now   int64
			valid bool
		}{
			{claims.Iat - 61, false}, {claims.Iat - 60, true}, {claims.Iat, true},
			{claims.Exp + 59, true}, {claims.Exp + 60, false},
		} {
			identity, err := p.Verifier.Verify(user.Token, time.Unix(tc.now, 0))
			if (err == nil) != tc.valid || (tc.valid && identity.AccountID != user.ID) {
				t.Fatal("native issuer/verifier clock boundary mismatch")
			}
		}
		body, _ := json.Marshal(psnAuthRequest(t, user.Token))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatal("native credential refused")
		}
		signature, err := base64.StdEncoding.DecodeString(w.Header().Get("X-Signature"))
		digest := sha256.Sum256(w.Body.Bytes())
		if err != nil || rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, digest[:], signature, nil) != nil {
			t.Fatal("response signature does not verify")
		}
		var response struct {
			AuthTask     string `json:"auth_task"`
			ServerTicket string `json:"server_ticket"`
		}
		ticket, err := base64.StdEncoding.DecodeString(func() string { _ = json.Unmarshal(w.Body.Bytes(), &response); return response.ServerTicket }())
		if err != nil || response.AuthTask != "47" {
			t.Fatal("unexpected publisher response")
		}
		id, ok := testTickets.IssuedTicketPlayer(ticket, time.Now(), ps4pkg.TicketWindow)
		if !ok || id.PID != user.ID || id.Username != user.Name || id.Title != 5760 {
			t.Fatal("issued ticket does not carry the verified identity")
		}
		l := handshake(t, ticket)
		testPresence.DropOnline(l.N)
		if l.Keys == nil || l.Player == nil || l.Player.PID != user.ID {
			t.Fatal("lobby did not admit the issued ticket")
		}
	}
}
