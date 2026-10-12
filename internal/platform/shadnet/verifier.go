// Package shadnet verifies the application-scoped NPAuth ID-token contract.
// Trust comes from local configuration, never from a URL or key in a token.
package shadnet

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	"github.com/NextendoNetwork/nextendo-demonware/internal/strictjson"
)

const clockLeewaySeconds int64 = 60

// The original CTR caller reads env_iss_id and a 64-character duid from the
// platform ID-token payload; 256 is the retail NP issuer environment.
const environmentIssuerID int64 = 256

var deviceID = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)

var errCredential = errors.New("shadNet credential refused")
var scopeName = regexp.MustCompile(`^[A-Za-z0-9:_./-]{1,64}$`)

// Policy names one trusted issuer, registered application and title. Scopes is
// the exact set accepted by this backend; token order is immaterial.
type Policy struct {
	Issuer    string   `json:"issuer"`
	ClientID  string   `json:"client_id"`
	NPTitleID string   `json:"np_title_id"`
	Scopes    []string `json:"scopes"`
}

// Verifier fixes its trust policy and safely shares keys across requests.
type Verifier struct {
	policy Policy
	scopes map[string]bool
	keys   map[string]*rsa.PublicKey
	remote *remoteKeys
}

var _ platform.Verifier = (*Verifier)(nil)

// New binds public keys to an explicit policy. It accepts only the public RSA
// JWKS emitted by shadNet, including overlapping keys during planned rotation.
func New(policy Policy, jwks []byte) (*Verifier, error) {
	v, err := newVerifier(policy)
	if err != nil {
		return nil, err
	}
	v.keys, err = parseKeys(jwks)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func newVerifier(policy Policy) (*Verifier, error) {
	u, err := url.Parse(policy.Issuer)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		!validText(policy.Issuer, 256) || !validText(policy.ClientID, 128) ||
		!validText(policy.NPTitleID, 64) || len(policy.Scopes) == 0 || len(policy.Scopes) > 32 {
		return nil, errors.New("invalid shadNet trust policy")
	}
	scopes := make(map[string]bool, len(policy.Scopes))
	for _, scope := range policy.Scopes {
		if !scopeName.MatchString(scope) || scopes[scope] {
			return nil, errors.New("invalid shadNet scope policy")
		}
		scopes[scope] = true
	}
	if !scopes["openid"] {
		return nil, errors.New("shadNet policy requires openid")
	}
	policy.Scopes = append([]string(nil), policy.Scopes...)
	return &Verifier{policy: policy, scopes: scopes}, nil
}

func parseKeys(jwks []byte) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if len(jwks) > 65536 || !decodeObject(jwks, &set, "keys") || len(set.Keys) == 0 || len(set.Keys) > 8 {
		return nil, errors.New("invalid shadNet public key set")
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, raw := range set.Keys {
		var jwk struct {
			N   string `json:"n"`
			E   string `json:"e"`
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			Use string `json:"use"`
		}
		if !decodeObject(raw, &jwk, "n", "e", "kty", "kid", "alg", "use") ||
			jwk.Kty != "RSA" || jwk.Alg != "RS256" || jwk.Use != "sig" ||
			!validText(jwk.Kid, 128) || keys[jwk.Kid] != nil {
			return nil, errors.New("unsupported or duplicate shadNet public key")
		}
		n, nOK := decodeBase64(jwk.N)
		e, eOK := decodeBase64(jwk.E)
		if !nOK || !eOK || len(n) < 256 || len(n) > 512 || n[0] == 0 || len(e) > 4 || e[0] == 0 {
			return nil, errors.New("invalid shadNet RSA parameters")
		}
		modulus := new(big.Int).SetBytes(n)
		exponent := new(big.Int).SetBytes(e).Int64()
		if modulus.BitLen() < 2048 || modulus.BitLen() > 4096 || modulus.Bit(0) != 1 ||
			exponent < 3 || exponent > math.MaxInt32 || exponent%2 != 1 {
			return nil, errors.New("unsupported shadNet RSA parameters")
		}
		keys[jwk.Kid] = &rsa.PublicKey{N: modulus, E: int(exponent)}
	}
	return keys, nil
}

// TrustID names the issuer, application and title this policy trusts. Account
// IDs are unique only within it, so an instance database records it and
// refuses another.
func (v *Verifier) TrustID() string {
	raw, _ := json.Marshal([]string{v.policy.Issuer, v.policy.ClientID, v.policy.NPTitleID})
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (v *Verifier) Verify(token string, now time.Time) (platform.Identity, error) {
	refuse := func() (platform.Identity, error) { return platform.Identity{}, errCredential }
	if v == nil || len(token) == 0 || len(token) > 4096 {
		return refuse()
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return refuse()
	}
	head, hOK := decodeBase64(parts[0])
	body, bOK := decodeBase64(parts[1])
	sig, sOK := decodeBase64(parts[2])
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}
	if !hOK || !bOK || !sOK || len(head) > 1024 ||
		!decodeObject(head, &header, "alg", "typ", "kid") || header.Alg != "RS256" || header.Typ != "JWT" || !validText(header.Kid, 128) {
		return refuse()
	}
	key := v.keys[header.Kid]
	if v.remote != nil {
		key = v.remote.key(header.Kid)
	}
	if key == nil || len(sig) != key.Size() {
		return refuse()
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return refuse()
	}
	var claims struct {
		Issuer      string `json:"iss"`
		Subject     string `json:"sub"`
		Audience    string `json:"aud"`
		IssuedAt    int64  `json:"iat"`
		Expiry      int64  `json:"exp"`
		ID          string `json:"jti"`
		Environment int64  `json:"env_iss_id"`
		OnlineID    string `json:"online_id"`
		NPTitleID   string `json:"np_title_id"`
		Scope       string `json:"scope"`
		DUID        string `json:"duid"`
	}
	names := []string{"iss", "sub", "aud", "iat", "exp", "jti", "env_iss_id", "online_id", "np_title_id", "scope"}
	if v.scopes["openid:duid"] {
		names = append(names, "duid")
	}
	if !decodeObject(body, &claims, names...) || claims.Environment != environmentIssuerID ||
		(v.scopes["openid:duid"] && !deviceID.MatchString(claims.DUID)) ||
		claims.Issuer != v.policy.Issuer || claims.Audience != v.policy.ClientID || claims.NPTitleID != v.policy.NPTitleID ||
		claims.IssuedAt <= 0 || claims.IssuedAt > math.MaxInt64-120-clockLeewaySeconds || claims.Expiry != claims.IssuedAt+120 ||
		now.Unix() < claims.IssuedAt-clockLeewaySeconds || now.Unix() >= claims.Expiry+clockLeewaySeconds || !validText(claims.OnlineID, 128) {
		return refuse()
	}
	accountID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || accountID <= 0 || strconv.FormatInt(accountID, 10) != claims.Subject {
		return refuse()
	}
	nonce, ok := decodeBase64(claims.ID)
	if !ok || len(nonce) != 16 || len(claims.Scope) > 1024 {
		return refuse()
	}
	scopes := strings.Split(claims.Scope, " ")
	if len(scopes) != len(v.scopes) {
		return refuse()
	}
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if !v.scopes[scope] || seen[scope] {
			return refuse()
		}
		seen[scope] = true
	}
	return platform.Identity{AccountID: uint64(accountID), OnlineID: claims.OnlineID, Subject: claims.Subject}, nil
}

func validText(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func decodeBase64(value string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return b, err == nil && len(b) > 0 && base64.RawURLEncoding.EncodeToString(b) == value
}

// Exact keys avoid encoding/json's case-insensitive field matching as well as
// ambiguous duplicates, trailing values, invalid UTF-8 and unknown extensions.
func decodeObject(raw []byte, out any, names ...string) bool {
	var fields map[string]json.RawMessage
	if !utf8.Valid(raw) || !strictjson.Decode(string(raw), &fields) || len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if value, ok := fields[name]; !ok || string(value) == "null" {
			return false
		}
	}
	return json.Unmarshal(raw, out) == nil
}
