package shadnet

import (
	"crypto/rsa"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const keyCacheLifetime = 5 * time.Minute
const keyRefreshInterval = 30 * time.Second

// Only the backend's configured endpoint supplies keys. Concurrent refreshes
// share one request; unknown key IDs cannot trigger an unbounded request stream.
type remoteKeys struct {
	url    string
	client *http.Client
	clock  func() time.Time

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
	retryAt time.Time
	loading chan struct{}
}

func newRemoteVerifier(policy Policy, endpoint string, client *http.Client) (*Verifier, error) {
	v, err := newVerifier(policy)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil || !validText(endpoint, 2048) || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, errors.New("JWKS requires a configured HTTPS or literal loopback HTTP endpoint")
	}
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	bounded.Timeout = 5 * time.Second
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r := &remoteKeys{url: endpoint, client: &bounded, clock: time.Now}
	keys, err := r.fetch()
	if err != nil {
		return nil, err
	}
	r.keys, r.expires = keys, r.clock().Add(keyCacheLifetime)
	v.remote = r
	return v, nil
}

func (r *remoteKeys) fetch() (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequest(http.MethodGet, r.url, nil)
	if err != nil {
		return nil, errors.New("invalid JWKS request")
	}
	request.Header.Set("Accept", "application/jwk-set+json, application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, errors.New("shadNet public key endpoint unavailable")
	}
	defer response.Body.Close()
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil ||
		(mediaType != "application/json" && mediaType != "application/jwk-set+json") {
		return nil, errors.New("invalid shadNet public key response")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return nil, errors.New("incomplete shadNet public key response")
	}
	return parseKeys(raw)
}

func (r *remoteKeys) key(id string) *rsa.PublicKey {
	for {
		r.mu.Lock()
		now := r.clock()
		if key := r.keys[id]; key != nil && now.Before(r.expires) {
			r.mu.Unlock()
			return key
		}
		if pending := r.loading; pending != nil {
			r.mu.Unlock()
			<-pending
			continue
		}
		if now.Before(r.retryAt) {
			r.mu.Unlock()
			return nil
		}
		r.loading = make(chan struct{})
		r.retryAt = now.Add(keyRefreshInterval)
		r.mu.Unlock()

		keys, err := r.fetch()
		r.mu.Lock()
		if err == nil {
			r.keys, r.expires = keys, r.clock().Add(keyCacheLifetime)
		}
		close(r.loading)
		r.loading = nil
		r.mu.Unlock()
		if err != nil {
			return nil
		}
	}
}
