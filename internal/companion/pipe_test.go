package companion

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"slices"
	"testing"
	"time"
)

func TestPipeOwnerRequestsAndEOF(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := New(context.Background(), inR, outW)
	defer p.Close()
	defer outR.Close()
	go func() {
		_ = json.NewEncoder(inW).Encode(map[string]any{"v": 1, "event": "owner", "steam_id": "76561198000000001", "online_id": "Player", "token": "osw1.76561198000000001.abababababababababababababababababababababababababababababababab." + base64.RawURLEncoding.EncodeToString([]byte("Player")), "title": "5760", "build": "01.21", "realm": "normal", "backend": "lab"})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Owner(ctx); err != nil {
		t.Fatal(err)
	}
	go func() {
		line, _ := bufio.NewReader(outR).ReadBytes('\n')
		var req map[string]any
		_ = json.Unmarshal(line, &req)
		_ = json.NewEncoder(inW).Encode(map[string]any{"v": 1, "id": req["id"], "ok": true, "result": map[string]string{"online_id": "Friend"}})
	}()
	var result struct {
		OnlineID string `json:"online_id"`
	}
	if err := p.Call(ctx, "name.resolve", map[string]any{"peer": "2"}, &result); err != nil || result.OnlineID != "Friend" {
		t.Fatal(err)
	}
	_ = inW.Close()
	select {
	case <-p.Done():
	case <-ctx.Done():
		t.Fatal("EOF did not stop pipe")
	}
	p.Wait()
}

func TestVirtualEndpointReadyCapabilityIsExplicit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		p := New(context.Background(), inR, outW)
		if enabled {
			p.EnableVirtualEndpoints()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := p.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		var ready struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := json.NewDecoder(outR).Decode(&ready); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(ready.Capabilities, "virtual.endpoints.v1") != enabled {
			t.Fatal("virtual capability not explicitly negotiated")
		}
		cancel()
		p.Close()
		inW.Close()
		outR.Close()
		p.Wait()
	}
}

func TestPipeRejectsMalformedOwner(t *testing.T) {
	for _, raw := range []string{`{"v":1,"v":1,"event":"owner"}`, `{"v":2,"event":"owner"}`, `{"v":1,"event":"ready"}`} {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		p := New(context.Background(), inR, outW)
		go func() { _, _ = io.WriteString(inW, raw+"\n"); inW.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err := p.Owner(ctx); err == nil {
			t.Fatal("malformed owner accepted")
		}
		cancel()
		p.Close()
		outR.Close()
		p.Wait()
	}
}
