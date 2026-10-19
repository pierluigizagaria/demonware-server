package companion

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"slices"
	"testing"
	"time"
)

func pendingPipe(t *testing.T, apply func(netip.AddrPort) error, ready bool) (*Pipe, *io.PipeWriter, *json.Decoder) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := New(context.Background(), inR, outW)
	t.Cleanup(func() { p.Close(); inW.Close(); outR.Close(); p.Wait() })
	go func() {
		_ = json.NewEncoder(inW).Encode(map[string]any{"v": 1, "event": "owner", "steam_id": "76561198000000001", "online_id": "Player", "token": "osw1.76561198000000001.abababababababababababababababababababababababababababababababab." + base64.RawURLEncoding.EncodeToString([]byte("Player")), "title": "5760", "build": "01.21", "realm": "normal", "backend": "lab", "app_id": 480, "transport": map[string]any{"mode": "loopback-v1", "address": "127.64.1.2", "port": 12000, "game_pending": true}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Owner(ctx); err != nil {
		t.Fatal(err)
	}
	p.EnableLoopbackEndpoints()
	p.EnableGameEndpoint(apply)
	decoder := json.NewDecoder(outR)
	if ready {
		if err := p.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		var response struct{ Capabilities []string }
		if decoder.Decode(&response) != nil || slices.Contains(response.Capabilities, "game.endpoint.v1") != (apply != nil) {
			t.Fatal("pending source capability mismatch")
		}
	}
	return p, inW, decoder
}

func endpointFrame() map[string]any {
	return map[string]any{"v": 1, "event": "game.endpoint", "id": "bind-1", "address": "127.0.0.1", "port": 3104}
}

func TestPendingGameEndpointCommitsBeforeExactAckAndCannotRebind(t *testing.T) {
	committed := make(chan netip.AddrPort, 1)
	p, input, decoder := pendingPipe(t, func(game netip.AddrPort) error { committed <- game; return nil }, true)
	for _, op := range []string{"directory.create", "directory.search", "peer.endpoint", "advertisement.set"} {
		if p.Call(context.Background(), op, nil, nil) == nil {
			t.Fatal("pending source permitted peer activity")
		}
	}
	if err := json.NewEncoder(input).Encode(endpointFrame()); err != nil {
		t.Fatal(err)
	}
	var ack map[string]any
	if decoder.Decode(&ack) != nil || ack["v"] != float64(1) || ack["event"] != "game.endpoint.bound" || ack["id"] != "bind-1" || ack["address"] != "127.0.0.1" || ack["port"] != float64(3104) || len(ack) != 5 {
		t.Fatal("binding acknowledgement changed")
	}
	select {
	case source := <-committed:
		if source.String() != "127.0.0.1:3104" {
			t.Fatal("wrong committed source")
		}
	default:
		t.Fatal("ack preceded commit")
	}
	_ = json.NewEncoder(input).Encode(endpointFrame())
	select {
	case <-p.Done():
		if p.CloseReason() != "game-endpoint" {
			t.Fatal("wrong close category")
		}
	case <-time.After(time.Second):
		t.Fatal("repeated binding did not close pipe")
	}
}

func TestGameEndpointMalformedOrUnnegotiatedClosesWithoutAck(t *testing.T) {
	for _, test := range []struct {
		name       string
		ready      bool
		applyError bool
		change     func(map[string]any)
	}{
		{"before-ready", false, false, nil},
		{"apply-refused", true, true, nil},
		{"foreign-address", true, false, func(f map[string]any) { f["address"] = "192.0.2.1" }},
		{"reserved-source", true, false, func(f map[string]any) { f["address"] = "127.64.1.3" }},
		{"zero-port", true, false, func(f map[string]any) { f["port"] = 0 }},
		{"bad-port-type", true, false, func(f map[string]any) { f["port"] = "3104" }},
		{"missing-id", true, false, func(f map[string]any) { delete(f, "id") }},
		{"extra-field", true, false, func(f map[string]any) { f["unknown"] = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, input, _ := pendingPipe(t, func(netip.AddrPort) error {
				if test.applyError {
					return errors.New("refused")
				}
				return nil
			}, test.ready)
			frame := endpointFrame()
			if test.change != nil {
				test.change(frame)
			}
			_ = json.NewEncoder(input).Encode(frame)
			select {
			case <-p.Done():
				if p.CloseReason() != "game-endpoint" {
					t.Fatal("wrong close category")
				}
			case <-time.After(time.Second):
				t.Fatal("invalid binding did not close pipe")
			}
		})
	}
}
