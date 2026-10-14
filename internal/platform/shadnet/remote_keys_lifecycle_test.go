package shadnet

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteKeysCloseCancelsAndJoinsRefresh(t *testing.T) {
	_, _, policy, jwks := issuerFixture(t)
	var requests atomic.Int64
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Write(jwks)
			return
		}
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	v, err := newRemoteVerifier(policy, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	r := v.remote
	r.mu.Lock()
	r.expires = time.Now().Add(keyRefreshAhead / 2)
	var id string
	for key := range r.keys {
		id = key
		break
	}
	r.mu.Unlock()
	if r.key(id) == nil {
		t.Fatal("valid cached key unavailable")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh not started")
	}
	done := make(chan struct{})
	go func() { v.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh was not cancelled")
	}
	if r.key(id) != nil || requests.Load() != 2 {
		t.Fatal("closed verifier started or admitted another refresh")
	}
}
