package shadnet

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteKeysCacheRotationAndConcurrentAdmission(t *testing.T) {
	_, first, policy, jwks := issuerFixture(t)
	var body atomic.Value
	body.Store(jwks)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/keys" || r.Header.Get("Authorization") != "" || r.ContentLength > 0 {
			t.Error("key retrieval sent unexpected request data")
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		w.Write(body.Load().([]byte))
	}))
	defer server.Close()
	v, err := newRemoteVerifier(policy, server.URL+"/keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	initialID := v.TrustID()
	for range 3 {
		if _, err := v.Verify(signToken(t, first, tokenClaims()), time.Unix(1001, 0)); err != nil {
			t.Fatal("cached key failed")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("cached verification fetched per token")
	}
	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var set, next map[string][]map[string]string
	json.Unmarshal(jwks, &set)
	json.Unmarshal(keySet(second), &next)
	next["keys"][0]["kid"] = "next-key"
	set["keys"] = append(set["keys"], next["keys"][0])
	overlap, _ := json.Marshal(set)
	body.Store(overlap)
	claims, _ := json.Marshal(tokenClaims())
	token := signRaw(t, second, []byte(`{"alg":"RS256","typ":"JWT","kid":"next-key"}`), claims)
	var wait sync.WaitGroup
	for range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := v.Verify(token, time.Unix(1001, 0)); err != nil {
				t.Error("new key was not downloaded for concurrent admission")
			}
		}()
	}
	wait.Wait()
	if requests.Load() != 2 || v.TrustID() != initialID {
		t.Fatal("rotation fanned out requests or changed the identity namespace")
	}
	if _, err := v.Verify(signToken(t, first, tokenClaims()), time.Unix(1001, 0)); err != nil {
		t.Fatal("published overlap key was dropped")
	}
	for range 10 {
		unknown := signRaw(t, second, []byte(`{"alg":"RS256","typ":"JWT","kid":"unknown"}`), claims)
		if _, err := v.Verify(unknown, time.Unix(1001, 0)); err == nil {
			t.Fatal("unknown signing key admitted")
		}
	}
	if requests.Load() != 2 {
		t.Fatal("unknown keys bypassed refresh throttling")
	}
}

func TestRemoteKeysOutageExpiryAndRecovery(t *testing.T) {
	_, key, policy, jwks := issuerFixture(t)
	var requests, status atomic.Int64
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(status.Load()))
		w.Write(jwks)
	}))
	defer server.Close()
	v, err := newRemoteVerifier(policy, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	v.remote.clock = func() time.Time { return now }
	token := signToken(t, key, tokenClaims())
	status.Store(http.StatusServiceUnavailable)
	claims, _ := json.Marshal(tokenClaims())
	unknown := signRaw(t, key, []byte(`{"alg":"RS256","typ":"JWT","kid":"unknown"}`), claims)
	if _, err := v.Verify(unknown, time.Unix(1001, 0)); err == nil {
		t.Fatal("outage admitted an unknown key")
	}
	if _, err := v.Verify(token, time.Unix(1001, 0)); err != nil {
		t.Fatal("failed refresh discarded still-fresh known keys")
	}
	now = now.Add(keyCacheLifetime + time.Second)
	if _, err := v.Verify(token, time.Unix(1001, 0)); err == nil {
		t.Fatal("outage extended expired key trust")
	}
	count := requests.Load()
	status.Store(http.StatusOK)
	if _, err := v.Verify(token, time.Unix(1001, 0)); err == nil || requests.Load() != count {
		t.Fatal("failed refresh retry was not bounded")
	}
	now = now.Add(keyRefreshInterval)
	if _, err := v.Verify(token, time.Unix(1001, 0)); err != nil || requests.Load() != count+1 {
		t.Fatal("key refresh did not recover without a restart")
	}
}

func TestRemoteKeysRejectUnsafeEndpointsAndResponses(t *testing.T) {
	_, _, policy, jwks := issuerFixture(t)
	for _, endpoint := range []string{"public.json", "file:///public.json", "http://issuer.example.test/keys", "http://localhost/keys", "https://user:pass@example.test/keys", "https://example.test/keys?query", "https://example.test/keys#fragment"} {
		if _, err := newRemoteVerifier(policy, endpoint, nil); err == nil {
			t.Fatal("unsafe key endpoint accepted")
		}
	}
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	for _, tc := range []struct {
		name, mediaType, body string
		status                int
	}{
		{"redirect", "application/json", string(jwks), http.StatusFound},
		{"error", "application/json", string(jwks), http.StatusServiceUnavailable},
		{"html", "text/html", string(jwks), http.StatusOK},
		{"oversized", "application/json", strings.Repeat(" ", 65537), http.StatusOK},
		{"malformed", "application/json", `{"keys":[]}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.mediaType)
				w.Header().Set("Location", target.URL)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			if _, err := newRemoteVerifier(policy, server.URL, nil); err == nil {
				t.Fatal("invalid remote key response admitted")
			}
		})
	}
	if targetRequests.Load() != 0 {
		t.Fatal("key retrieval followed a redirect")
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(jwks)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	if _, err := newRemoteVerifier(policy, server.URL, nil); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	if _, err := newRemoteVerifier(policy, server.URL, server.Client()); err != nil {
		t.Fatal("trusted HTTPS key endpoint failed")
	}
}

func TestRemoteKeysRefreshTimeoutAndHeaderValidation(t *testing.T) {
	_, key, policy, jwks := issuerFixture(t)
	var blocked atomic.Bool
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if blocked.Load() {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(jwks)
	}))
	defer server.Close()
	v, err := newRemoteVerifier(policy, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(tokenClaims())
	for _, header := range []string{
		`{"alg":"none","typ":"JWT","kid":"unknown"}`,
		`{"alg":"RS256","typ":"JWT","kid":"unknown","jku":"http://untrusted.test"}`,
		`{"alg":"RS256","typ":"JWT","kid":""}`,
	} {
		if _, err := v.Verify(signRaw(t, key, []byte(header), claims), time.Unix(1001, 0)); err == nil {
			t.Fatal("invalid JWT header admitted")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("invalid headers triggered network requests")
	}
	blocked.Store(true)
	v.remote.client.Timeout = 30 * time.Millisecond
	unknown := signRaw(t, key, []byte(`{"alg":"RS256","typ":"JWT","kid":"unknown"}`), claims)
	if _, err := v.Verify(unknown, time.Unix(1001, 0)); err == nil {
		t.Fatal("timed-out refresh admitted unknown key")
	}
	if _, err := v.Verify(signToken(t, key, tokenClaims()), time.Unix(1001, 0)); err != nil {
		t.Fatal("timed-out refresh prevented cached verification")
	}
}

func TestRemoteKeysRefreshAheadDoesNotBlockAdmission(t *testing.T) {
	_, key, policy, jwks := issuerFixture(t)
	var requests atomic.Int64
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 1 {
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(jwks)
	}))
	defer server.Close()
	v, err := newRemoteVerifier(policy, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := v.remote.expires.Add(-keyRefreshAhead / 2)
	v.remote.clock = func() time.Time { return now }
	token := signToken(t, key, tokenClaims())
	done := make(chan error, 1)
	go func() {
		_, err := v.Verify(token, time.Unix(1001, 0))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("cached key refused during background refresh")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("admission waited for the key endpoint")
	}
	for range 5 {
		if _, err := v.Verify(token, time.Unix(1001, 0)); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		v.remote.mu.Lock()
		renewed := v.remote.loading == nil && v.remote.expires.Equal(now.Add(keyCacheLifetime))
		v.remote.mu.Unlock()
		if renewed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not renew the key set")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if requests.Load() != 2 {
		t.Fatalf("refresh-ahead made %d requests", requests.Load())
	}
}
