package steam

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func envelope(token string) []byte {
	inner, _ := json.Marshal(map[string]string{"id_token": token})
	extra, _ := json.Marshal(map[string]string{"token": string(inner)})
	body, _ := json.Marshal(map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "1", "extra_data": string(extra)})
	return body
}

const fixtureToken = "osw1.76561198000000001.0123abcd.UGxheWVyQQ"

func TestTokenCanonicalIdentityAndReplayHash(t *testing.T) {
	a, err := ParseToken(fixtureToken)
	if err != nil || a.AccountID != 76561198000000001 || a.OnlineID != "PlayerA" {
		t.Fatal("valid token refused")
	}
	b, err := ParseToken(strings.Replace(fixtureToken, "abcd", "ABCD", 1))
	if err != nil || a.Hash != b.Hash {
		t.Fatal("hex case bypasses replay identity")
	}
	for _, value := range []string{"", strings.Repeat("a", 4097), strings.Replace(fixtureToken, "7656", "07656", 1),
		strings.Replace(fixtureToken, "0123abcd", "", 1), strings.Replace(fixtureToken, "0123abcd", "abc", 1),
		fixtureToken + "=", fixtureToken + ".extra", "osw1.1.00." + base64.RawURLEncoding.EncodeToString([]byte("bad.name"))} {
		if _, err := ParseToken(value); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}

type validatorStub struct {
	result Validation
	err    error
	ends   int
}

func (v *validatorStub) Validate(context.Context, string) (Validation, error) { return v.result, v.err }
func (v *validatorStub) End(uint64)                                           { v.ends++ }
func (*validatorStub) ServerID() uint64                                       { return 99 }
func (*validatorStub) Close() error                                           { return nil }

type storeStub struct {
	used bool
	err  error
}

func (s *storeStub) SteamTicketUsed([32]byte) (bool, error) { return s.used, nil }
func (s *storeStub) RecordSteamAdmission([32]byte, uint64, string, time.Time) error {
	if s.err == nil {
		s.used = true
	}
	return s.err
}

func TestVerifiedIdentityAndConnectionOwnership(t *testing.T) {
	v := &validatorStub{result: Validation{76561198000000001, 76561198000000001, 1}}
	s := &storeStub{}
	p := &Platform{Validator: v, Store: s, Realm: "normal"}
	id, err := p.Authenticate(envelope(fixtureToken))
	if err != nil || id.Kind != "steam" || id.Sub != "steam:76561198000000001" || id.Title != 5760 {
		t.Fatal("verified identity missing")
	}
	if v.ends != 0 || !id.Admission.Claim() {
		t.Fatal("native session not transferred")
	}
	id.Admission.Expire()
	if v.ends != 0 {
		t.Fatal("active session ended by admission expiry")
	}
	id.Admission.Close()
	id.Admission.Close()
	if v.ends != 1 {
		t.Fatal("connection close did not end exactly once")
	}
	if _, err := p.Authenticate(envelope(fixtureToken)); err == nil {
		t.Fatal("replayed credential admitted")
	}
}

func TestAdmissionFailsClosedAndReleasesRejectedBinding(t *testing.T) {
	for _, result := range []Validation{{7, 7, 1}, {76561198000000001, 0, 1}, {76561198000000001, 76561198000000001, 1}} {
		v := &validatorStub{result: result}
		p := &Platform{Validator: v, Store: &storeStub{err: errors.New("name conflict")}}
		if _, err := p.Authenticate(envelope(fixtureToken)); err == nil || v.ends != 1 {
			t.Fatal("refused identity leaked admission")
		}
	}
	p := &Platform{Validator: &validatorStub{err: errors.New("down")}, Store: &storeStub{}}
	if _, err := p.Authenticate(envelope(fixtureToken)); err == nil {
		t.Fatal("helper failure admitted")
	}
	p.Store = nil
	if _, err := p.Authenticate(envelope(fixtureToken)); err == nil {
		t.Fatal("missing store admitted")
	}
}
