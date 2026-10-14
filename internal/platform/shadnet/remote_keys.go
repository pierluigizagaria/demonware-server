package shadnet

import (
	"context"
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
const keyRefreshAhead = time.Minute

// Only the backend's configured endpoint supplies keys. Concurrent refreshes
// share one request; unknown key IDs cannot trigger an unbounded request stream.
type remoteKeys struct {
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	workers sync.WaitGroup
	url     string
	client  *http.Client
	clock   func() time.Time

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
	if err := checkEndpoint(endpoint); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	bounded.Timeout = 5 * time.Second
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithCancel(context.Background())
	r := &remoteKeys{ctx: ctx, cancel: cancel, url: endpoint, client: &bounded, clock: time.Now}
	keys, err := r.fetch()
	if err != nil {
		cancel()
		return nil, err
	}
	r.keys, r.expires = keys, r.clock().Add(keyCacheLifetime)
	v.remote = r
	return v, nil
}

func checkEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || !validText(endpoint, 2048) || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback())) {
		return errors.New("JWKS requires a configured HTTPS or literal loopback HTTP endpoint")
	}
	return nil
}

func (r *remoteKeys) fetch() (map[string]*rsa.PublicKey, error) {
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
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
		if r.closed {
			r.mu.Unlock()
			return nil
		}
		now := r.clock()
		if key := r.keys[id]; key != nil && now.Before(r.expires) {
			// Renew shortly before expiry in the background, so admission never
			// waits for the endpoint while the cached keys are still valid.
			if r.loading == nil && !now.Before(r.expires.Add(-keyRefreshAhead)) && !now.Before(r.retryAt) {
				r.beginRefresh(now)
				go r.refresh(now)
			}
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
		r.beginRefresh(now)
		r.mu.Unlock()
		if !r.refresh(now) {
			return nil
		}
	}
}

// beginRefresh claims the single refresh; r.mu must be held.
func (r *remoteKeys) beginRefresh(now time.Time) {
	r.workers.Add(1)
	r.loading = make(chan struct{})
	r.retryAt = now.Add(keyRefreshInterval)
}

// refresh downloads the key set and releases waiting admissions. A failure
// keeps the current keys until they expire; the lifetime counts from started.
func (r *remoteKeys) refresh(started time.Time) bool {
	defer r.workers.Done()
	keys, err := r.fetch()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.keys, r.expires = keys, started.Add(keyCacheLifetime)
	}
	close(r.loading)
	r.loading = nil
	return err == nil
}

// Close prevents further refreshes, cancels the owned HTTP request and joins
// any synchronous or background refresh before the instance releases resources.
func (v *Verifier) Close() error {
	if v.remote == nil {
		return nil
	}
	r := v.remote
	r.mu.Lock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	r.workers.Wait()
	r.client.CloseIdleConnections()
	return nil
}
