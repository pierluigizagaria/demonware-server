package shadnet

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func issuerFixture(t *testing.T) (*Verifier, *rsa.PrivateKey, Policy, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{"https://issuer.example.test", "test-app", "CUSA00000", []string{"openid", "openid:duid"}}
	jwks := keySet(key)
	v, err := New(policy, jwks)
	if err != nil {
		t.Fatal(err)
	}
	return v, key, policy, jwks
}

func keySet(key *rsa.PrivateKey) []byte {
	raw, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "fixture-key",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
	return raw
}

func tokenClaims() map[string]any {
	return map[string]any{"iss": "https://issuer.example.test", "aud": "test-app", "sub": "17",
		"iat": 1000, "exp": 1120, "jti": base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		"online_id": "FixtureUser", "np_title_id": "CUSA00000", "scope": "openid:duid openid",
		"env_iss_id": 256, "duid": strings.Repeat("0123456789abcdef", 4)}
}

func signRaw(t *testing.T, key *rsa.PrivateKey, head, body []byte) string {
	t.Helper()
	message := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func signToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return signRaw(t, key, []byte(`{"alg":"RS256","typ":"JWT","kid":"fixture-key"}`), body)
}

func TestVerifyShadNetIdentityAndAdmissionLifetime(t *testing.T) {
	v, key, policy, jwks := issuerFixture(t)
	token := signToken(t, key, tokenClaims())
	// The verifier owns its policy and parsed keys after construction.
	policy.Scopes[0] = "changed"
	jwks[0] = '!'
	for _, now := range []int64{940, 999, 1000, 1001, 1119, 1120, 1179} {
		id, err := v.Verify(token, time.Unix(now, 0))
		if err != nil || id.AccountID != 17 || id.Subject != "17" || id.OnlineID != "FixtureUser" {
			t.Fatal("valid issuer identity lost")
		}
	}
	for _, now := range []int64{939, 1180, 9999} {
		if _, err := v.Verify(token, time.Unix(now, 0)); err == nil {
			t.Fatal("credential admitted outside its lifetime")
		}
	}
	// Repeated admission while fresh is allowed; this is not a one-use code.
	if _, err := v.Verify(token, time.Unix(1001, 0)); err != nil {
		t.Fatal("fresh retry refused")
	}
}

func TestVerifyRejectsWrongClaims(t *testing.T) {
	v, key, _, _ := issuerFixture(t)
	cases := []struct {
		name, field string
		value       any
	}{
		{"issuer", "iss", "https://other.example.test"}, {"audience", "aud", "other-app"},
		{"array audience", "aud", []string{"test-app"}}, {"title", "np_title_id", "CUSA99999"},
		{"prefixed subject", "sub", "shadnet:17"}, {"zero subject", "sub", "0"},
		{"leading zero", "sub", "017"}, {"numeric subject", "sub", 17},
		{"signed subject", "sub", "+17"}, {"overflow subject", "sub", "9223372036854775808"},
		{"empty name", "online_id", ""}, {"control name", "online_id", "bad\nname"},
		{"unknown scope", "scope", "openid unknown"}, {"missing scope", "scope", "openid"},
		{"duplicate scope", "scope", "openid openid"}, {"comma scopes", "scope", "openid,openid:duid"},
		{"extra scope", "scope", "openid openid:duid extra"}, {"empty scope item", "scope", "openid  openid:duid"},
		{"future issuance", "iat", 1002}, {"long lifetime", "exp", 1121},
		{"fractional time", "iat", 1000.5}, {"null time", "iat", nil},
		{"time overflow", "iat", json.Number("9223372036854775807")},
		{"nonce", "jti", "short"}, {"unexpected nbf", "nbf", 1000}, {"legacy title", "title_id", "5760"},
		{"other environment", "env_iss_id", 1}, {"string environment", "env_iss_id", "256"},
		{"short duid", "duid", "0123456789abcdef"}, {"non-hex duid", "duid", strings.Repeat("z", 64)},
		{"numeric duid", "duid", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := tokenClaims()
			claims[tc.field] = tc.value
			if _, err := v.Verify(signToken(t, key, claims), time.Unix(1001, 0)); err == nil {
				t.Fatal("invalid claim accepted")
			}
		})
	}
	for field := range tokenClaims() {
		claims := tokenClaims()
		delete(claims, field)
		if _, err := v.Verify(signToken(t, key, claims), time.Unix(1001, 0)); err == nil {
			t.Fatalf("missing %s accepted", field)
		}
	}
}

func TestVerifyDeviceClaimFollowsGrantedScope(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(Policy{"https://issuer.example.test", "test-app", "CUSA00000", []string{"openid"}}, keySet(key))
	if err != nil {
		t.Fatal(err)
	}
	claims := tokenClaims()
	claims["scope"] = "openid"
	delete(claims, "duid")
	if _, err := v.Verify(signToken(t, key, claims), time.Unix(1001, 0)); err != nil {
		t.Fatal("openid-only credential refused")
	}
	claims["duid"] = strings.Repeat("ab", 32)
	if _, err := v.Verify(signToken(t, key, claims), time.Unix(1001, 0)); err == nil {
		t.Fatal("device claim accepted without its granted scope")
	}
	// The original caller's hex parser accepts either letter case.
	strict, strictKey, _, _ := issuerFixture(t)
	upper := tokenClaims()
	upper["duid"] = strings.Repeat("AB", 32)
	if _, err := strict.Verify(signToken(t, strictKey, upper), time.Unix(1001, 0)); err != nil {
		t.Fatal("upper-case device identifier refused")
	}
}

func TestVerifyRejectsHeaderKeyAndEncodingConfusion(t *testing.T) {
	v, key, _, _ := issuerFixture(t)
	body, _ := json.Marshal(tokenClaims())
	for _, head := range []string{
		`{"alg":"none","typ":"JWT","kid":"fixture-key"}`,
		`{"alg":"HS256","typ":"JWT","kid":"fixture-key"}`,
		`{"alg":"RS256","typ":"JWT","kid":"unknown"}`,
		`{"alg":"RS256","typ":"JWT","kid":"fixture-key","jku":"https://untrusted.invalid/keys"}`,
		`{"alg":"RS256","typ":"JWT","kid":"fixture-key","crit":["custom"]}`,
		`{"alg":"RS256","typ":"JWT","kid":"fixture-key","kid":"fixture-key"}`,
		`{"Alg":"RS256","typ":"JWT","kid":"fixture-key"}`,
		`{"alg":"RS256","typ":"other","kid":"fixture-key"}`,
	} {
		if _, err := v.Verify(signRaw(t, key, []byte(head), body), time.Unix(1001, 0)); err == nil {
			t.Fatal("unsafe header accepted")
		}
	}
	validHead := []byte(`{"alg":"RS256","typ":"JWT","kid":"fixture-key"}`)
	for _, changed := range [][]byte{
		[]byte(strings.Replace(string(body), `"sub":"17"`, `"sub":"17","sub":"18"`, 1)),
		[]byte(strings.Replace(string(body), `"sub":`, `"Sub":`, 1)),
		append(append([]byte{}, body...), []byte(`{}`)...),
		[]byte(strings.Replace(string(body), "FixtureUser", string([]byte{0xff}), 1)),
	} {
		if _, err := v.Verify(signRaw(t, key, validHead, changed), time.Unix(1001, 0)); err == nil {
			t.Fatal("ambiguous claims accepted")
		}
	}
	token := signToken(t, key, tokenClaims())
	parts := strings.Split(token, ".")
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	signature[0] ^= 1
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	for _, malformed := range []string{"", "a.b", token + "=", strings.Join(parts, "."), "\n" + token, strings.Repeat("x", 4097)} {
		if _, err := v.Verify(malformed, time.Unix(1001, 0)); err == nil {
			t.Fatal("malformed or forged token accepted")
		}
	}
}

func TestTrustConfigurationAndRotation(t *testing.T) {
	v, key, policy, jwks := issuerFixture(t)
	var set map[string][]map[string]string
	json.Unmarshal(jwks, &set)
	oldKey := set["keys"][0]
	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var next map[string][]map[string]string
	json.Unmarshal(keySet(second), &next)
	next["keys"][0]["kid"] = "rotated-key"
	set["keys"] = append(set["keys"], next["keys"][0])
	rotated, _ := json.Marshal(set)
	v2, err := New(policy, rotated)
	if err != nil || v2.TrustID() != v.TrustID() {
		t.Fatal("key rotation changed identity namespace")
	}
	if _, err := v2.Verify(signToken(t, key, tokenClaims()), time.Unix(1001, 0)); err != nil {
		t.Fatal("overlapping old key refused")
	}
	body, _ := json.Marshal(tokenClaims())
	token := signRaw(t, second, []byte(`{"alg":"RS256","typ":"JWT","kid":"rotated-key"}`), body)
	if _, err := v2.Verify(token, time.Unix(1001, 0)); err != nil {
		t.Fatal("new key refused")
	}
	if _, err := v.Verify(token, time.Unix(1001, 0)); err == nil {
		t.Fatal("unconfigured rotation key trusted")
	}
	for _, field := range []string{"issuer", "application", "title"} {
		p := policy
		switch field {
		case "issuer":
			p.Issuer = "https://other.example.test"
		case "application":
			p.ClientID = "other"
		case "title":
			p.NPTitleID = "CUSA99999"
		}
		other, err := New(p, jwks)
		if err != nil || other.TrustID() == v.TrustID() {
			t.Fatal("distinct trust domains share a ticket namespace")
		}
	}
	for _, mutate := range []func(map[string]string){
		func(k map[string]string) { k["alg"] = "HS256" }, func(k map[string]string) { k["use"] = "enc" },
		func(k map[string]string) { k["kty"] = "EC" }, func(k map[string]string) { k["d"] = "private" },
		func(k map[string]string) { k["e"] = "" }, func(k map[string]string) { k["e"] = "Ag" },
		func(k map[string]string) { k["n"] = "AA" + k["n"] }, func(k map[string]string) { k["kid"] = "" },
	} {
		copyKey := map[string]string{}
		for k, value := range oldKey {
			copyKey[k] = value
		}
		mutate(copyKey)
		raw, _ := json.Marshal(map[string]any{"keys": []any{copyKey}})
		if _, err := New(policy, raw); err == nil {
			t.Fatal("unsafe public key accepted")
		}
	}
	duplicate, _ := json.Marshal(map[string]any{"keys": []any{oldKey, oldKey}})
	for _, raw := range [][]byte{duplicate, []byte(`{"keys":[]}`), []byte(`null`), append(jwks, []byte(`{}`)...)} {
		if _, err := New(policy, raw); err == nil {
			t.Fatal("invalid key set accepted")
		}
	}
}

func TestLoadRemoteTrustConfiguration(t *testing.T) {
	_, _, policy, jwks := issuerFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" || r.Method != http.MethodGet {
			t.Error("unexpected key request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(jwks)
	}))
	defer server.Close()
	jwksURL := server.URL + "/.well-known/jwks.json"
	if err := Check(policy, jwksURL); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(policy, jwksURL); err != nil {
		t.Fatal("remote public key endpoint refused")
	}
	for _, change := range []func(p *Policy, url *string){
		func(p *Policy, _ *string) { p.Issuer = "http://issuer.example.test" },
		func(p *Policy, _ *string) { p.Issuer = "https://user:pass@example.test" },
		func(p *Policy, _ *string) { p.Issuer = "https://issuer.example.test/?query" },
		func(p *Policy, _ *string) { p.ClientID = "" },
		func(p *Policy, _ *string) { p.NPTitleID = "" },
		func(p *Policy, _ *string) { p.Scopes = []string{"openid", "openid"} },
		func(p *Policy, _ *string) { p.Scopes = []string{"openid:duid"} },
		func(p *Policy, _ *string) { p.Scopes = []string{"openid", "bad scope"} },
		func(p *Policy, _ *string) { p.Scopes = nil },
		func(_ *Policy, url *string) { *url = "public.json" },
		func(_ *Policy, url *string) { *url = "" },
	} {
		p, url := policy, jwksURL
		p.Scopes = append([]string(nil), policy.Scopes...)
		change(&p, &url)
		if Check(p, url) == nil {
			t.Fatal("invalid trust configuration passed the check")
		}
		if _, err := Load(p, url); err == nil {
			t.Fatal("invalid trust configuration accepted")
		}
	}
}

func TestClockLeewayDoesNotExtendSignedLifetime(t *testing.T) {
	v, key, _, _ := issuerFixture(t)
	for _, tc := range []struct {
		name          string
		iat, exp, now int64
		valid         bool
	}{
		{"future within allowance", 1061, 1181, 1001, true},
		{"future beyond allowance", 1062, 1182, 1001, false},
		{"expired within allowance", 881, 1001, 1060, true},
		{"expired at boundary", 881, 1001, 1061, false},
		{"long signed lifetime", 1000, 1180, 1001, false},
		{"short signed lifetime", 1000, 1119, 1001, false},
		{"overflow in expiry allowance", 9223372036854775687, 9223372036854775807, 9223372036854775750, false},
		{"negative verification clock", 1000, 1120, -9223372036854775808, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := tokenClaims()
			claims["iat"], claims["exp"] = tc.iat, tc.exp
			_, err := v.Verify(signToken(t, key, claims), time.Unix(tc.now, 0))
			if (err == nil) != tc.valid {
				t.Fatal("unexpected bounded clock-skew decision")
			}
		})
	}
}
