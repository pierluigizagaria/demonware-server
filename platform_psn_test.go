package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/platform"
)

type platformVerifierFunc func(string, time.Time) (platform.Identity, error)

func (f platformVerifierFunc) Verify(token string, now time.Time) (platform.Identity, error) {
	return f(token, now)
}

func psnAuthRequest(t *testing.T, token string) map[string]string {
	t.Helper()
	envelope, err := json.Marshal(map[string]string{"id_token": token})
	if err != nil {
		t.Fatal(err)
	}
	extra, err := json.Marshal(map[string]string{"token": string(envelope), "username": "untrusted"})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "4294967295", "extra_data": string(extra)}
}

func TestPSNPlatformUsesVerifiedIdentityAndOriginalEnvelope(t *testing.T) {
	calls := 0
	p := psnPlatform{verifier: platformVerifierFunc(func(token string, now time.Time) (platform.Identity, error) {
		calls++
		if token != "synthetic-fixture" || now.IsZero() {
			t.Fatal("credential envelope or admission clock lost")
		}
		return platform.Identity{AccountID: 17, OnlineID: "verified-user", Subject: "17"}, nil
	})}
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	id, err := p.Authenticate(body, "127.0.0.1")
	if err != nil || calls != 1 || id.PID != 17 || id.Username != "verified-user" || id.Sub != "shadnet:17" || id.Title != 5760 || id.Kind != "shadnet" {
		t.Fatal("verified identity did not reach the publisher boundary")
	}
	data := p.AuthData(id)
	if len(data) != 1 || data["extended_data"] != "nextendo:shadnet:17" {
		t.Fatal("PSN response changed or included Nintendo platform data")
	}
}

func TestPSNPlatformRejectsInvalidEnvelopesBeforeVerification(t *testing.T) {
	p := psnPlatform{verifier: platformVerifierFunc(func(string, time.Time) (platform.Identity, error) {
		t.Fatal("invalid envelope reached the verifier")
		return platform.Identity{}, nil
	})}
	cases := []struct{ name, field, value string }{
		{"Nintendo title", "title_id", "5775"},
		{"Nintendo task", "auth_task", "78"},
		{"overflow seed", "iv_seed", "4294967296"},
		{"negative seed", "iv_seed", "-1"},
		{"missing seed", "iv_seed", ""},
		{"invalid extra", "extra_data", "{"},
		{"unwrapped token", "extra_data", `{"token":"synthetic-fixture"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := psnAuthRequest(t, "synthetic-fixture")
			req[tc.field] = tc.value
			body, _ := json.Marshal(req)
			if _, err := p.Authenticate(body, "127.0.0.1"); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
	body, _ := json.Marshal(psnAuthRequest(t, strings.Repeat("x", 4097)))
	if _, err := p.Authenticate(body, "127.0.0.1"); err == nil {
		t.Fatal("oversized token accepted")
	}
}

func TestPlatformAdmissionFailsClosedWithoutNintendoFallback(t *testing.T) {
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	p := psnPlatform{verifier: platformVerifierFunc(func(string, time.Time) (platform.Identity, error) {
		return platform.Identity{}, errors.New("refused")
	})}
	if id, err := p.Authenticate(body, "127.0.0.1"); err == nil || id.PID != 0 || id.Kind != "" {
		t.Fatal("failed platform verification fell through to another identity path")
	}
	if _, err := (psnPlatform{}).Authenticate(body, "127.0.0.1"); err == nil {
		t.Fatal("missing verifier admitted a player")
	}
}
