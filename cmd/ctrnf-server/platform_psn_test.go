package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
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
