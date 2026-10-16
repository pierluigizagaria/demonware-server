package companion

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func diagnosticOwner() string {
	raw, _ := json.Marshal(map[string]any{
		"v": 1, "event": "owner", "steam_id": "76561198000000001", "online_id": "Player",
		"token": "osw1.76561198000000001." + strings.Repeat("ab", 32) + "." + base64.RawURLEncoding.EncodeToString([]byte("Player")),
		"title": "5760", "build": "1.21", "realm": "normal", "backend": "lab",
	})
	return string(raw) + "\n"
}

func diagnosticPipe(t *testing.T, ctx context.Context) (*Pipe, *io.PipeWriter, *io.PipeReader) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := New(ctx, inR, outW)
	t.Cleanup(func() { p.Close(); inW.Close(); outR.Close(); p.Wait() })
	return p, inW, outR
}

func requireCloseReason(t *testing.T, p *Pipe, want string) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("pipe did not close")
	}
	if got := p.CloseReason(); got != want {
		t.Fatalf("close category = %q, want %q", got, want)
	}
	// Teardown wakes the other worker, whose EOF/I/O error must not replace
	// the cause already visible when Done closed.
	p.Close()
	p.Wait()
	if got := p.CloseReason(); got != want {
		t.Fatalf("teardown changed category to %q", got)
	}
}

func TestPipeCloseReasonProtocolAndEOF(t *testing.T) {
	owner := diagnosticOwner()
	for _, test := range []struct{ name, raw, want string }{
		{"eof", owner, "eof"},
		{"malformed", owner + "private-frame-canary\n", "malformed-frame"},
		{"duplicate-field", owner + "{\"v\":1,\"v\":1,\"event\":\"private-canary\"}\n", "malformed-frame"},
		{"invalid-utf8", owner + "\xff\n", "malformed-frame"},
		{"version", owner + "{\"v\":2,\"private-canary\":true}\n", "version"},
		{"owner", "{\"v\":1,\"event\":\"private-canary\"}\n", "owner"},
		{"owner-name", owner + "{\"v\":1,\"event\":\"owner.name\",\"steam_id\":\"private-id-canary\",\"online_id\":\"private-name-canary\"}\n", "owner-name"},
		{"response", owner + "{\"v\":1,\"id\":99,\"private-canary\":true}\n", "response"},
		{"event", owner + "{\"v\":1,\"event\":\"private-event-canary\"}\n", "event"},
		{"frame-limit", owner + strings.Repeat("private-canary", MaxFrame/14+1) + "\n", "frame-limit"},
		{"event-queue-full", owner + strings.Repeat("{\"v\":1,\"event\":\"room.closed\"}\n", 129), "event-queue-full"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, in, _ := diagnosticPipe(t, context.Background())
			if p.CloseReason() != "open" {
				t.Fatal("new pipe is not open")
			}
			go func() { _, _ = io.WriteString(in, test.raw); _ = in.Close() }()
			requireCloseReason(t, p, test.want)
		})
	}
}

func TestPipeCloseReasonDuplicateResponse(t *testing.T) {
	p, in, _ := diagnosticPipe(t, context.Background())
	// A pending consumer has not yet consumed its first response. Deliver a
	// second correlated frame through the actual reader before allowing it to.
	p.mu.Lock()
	p.pending["fixture"] = make(chan response, 1)
	p.mu.Unlock()
	go func() {
		_, _ = io.WriteString(in, diagnosticOwner()+strings.Repeat("{\"v\":1,\"id\":\"fixture\",\"ok\":true}\n", 2))
		_ = in.Close()
	}()
	requireCloseReason(t, p, "duplicate-response")
}

type diagnosticReaderError struct{}

func (diagnosticReaderError) Read([]byte) (int, error) {
	return 0, errors.New("private-read-error-canary")
}
func (diagnosticReaderError) Close() error { return nil }

type diagnosticWriterError struct{ partial bool }

func (w diagnosticWriterError) Write(raw []byte) (int, error) {
	if w.partial {
		return len(raw) - 1, nil
	}
	return 0, errors.New("private-write-error-canary")
}
func (diagnosticWriterError) Close() error { return nil }

func TestPipeCloseReasonIO(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		out, writer := io.Pipe()
		defer out.Close()
		p := New(context.Background(), diagnosticReaderError{}, writer)
		defer p.Close()
		requireCloseReason(t, p, "read-error")
	})
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "short-write"}[partial], func(t *testing.T) {
			input, writer := io.Pipe()
			defer writer.Close()
			p := New(context.Background(), input, diagnosticWriterError{partial})
			defer p.Close()
			if err := p.Ready(context.Background()); err != nil {
				t.Fatal("ready could not be queued")
			}
			requireCloseReason(t, p, "write-error")
		})
	}
}

func TestPipeCloseReasonCancellation(t *testing.T) {
	t.Run("requested", func(t *testing.T) {
		p, _, _ := diagnosticPipe(t, context.Background())
		p.Close()
		requireCloseReason(t, p, "requested")
	})
	t.Run("parent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p, _, _ := diagnosticPipe(t, ctx)
		cancel()
		requireCloseReason(t, p, "context-canceled")
	})
}

func TestPipeProtocolFailureReleasesPendingRequest(t *testing.T) {
	p, in, out := diagnosticPipe(t, context.Background())
	go func() { _, _ = io.WriteString(in, diagnosticOwner()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := p.Owner(ctx); err != nil {
		t.Fatal("owner not accepted")
	}
	completed := make(chan error, 1)
	go func() { completed <- p.Call(ctx, "directory.members", nil, nil) }()
	// The actual pending request has reached the parent before its input fails.
	if _, err := bufio.NewReader(out).ReadBytes('\n'); err != nil {
		t.Fatal("request not written")
	}
	go func() { _, _ = io.WriteString(in, "private-frame-canary\n") }()
	requireCloseReason(t, p, "malformed-frame")
	select {
	case err := <-completed:
		if err == nil {
			t.Fatal("protocol failure completed a request successfully")
		}
	case <-ctx.Done():
		t.Fatal("protocol failure left a request blocked")
	}
}
