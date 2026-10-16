package companion

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform/local"
)

func TestOwnerNameNegotiationAndValidation(t *testing.T) {
	for _, mode := range []string{"valid", "legacy", "before-ready", "foreign-owner", "invalid-name", "extra-field"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			inR, inW := io.Pipe()
			outR, outW := io.Pipe()
			p := New(ctx, inR, outW)
			defer func() { p.Close(); inW.Close(); outR.Close(); p.Wait() }()
			owner := local.Owner{SteamID: "76561198000000001", OnlineID: "Initial", Token: "osw1.76561198000000001.abababababababababababababababababababababababababababababababab." + base64.RawURLEncoding.EncodeToString([]byte("Initial")), Title: "5760", Build: "1.21", Realm: "normal", Backend: "lab"}
			raw, _ := json.Marshal(owner)
			var hello map[string]any
			json.Unmarshal(raw, &hello)
			hello["v"] = 1
			hello["event"] = "owner"
			go json.NewEncoder(inW).Encode(hello)
			got, err := p.Owner(ctx)
			if err != nil {
				t.Fatal(err)
			}
			platform, err := local.New(got)
			if err != nil {
				t.Fatal(err)
			}
			applied := make(chan bool, 1)
			if mode != "legacy" {
				p.EnableOwnerNames(func(id, name string) error {
					_, err := platform.Rename(id, name)
					if err == nil {
						applied <- true
					}
					return err
				})
			}
			if mode != "before-ready" {
				sent := make(chan map[string]any, 1)
				go func() {
					line, _ := bufio.NewReader(outR).ReadBytes('\n')
					var ready map[string]any
					json.Unmarshal(line, &ready)
					sent <- ready
				}()
				if err := p.Ready(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case ready := <-sent:
					caps, ok := ready["capabilities"].([]any)
					if !ok || (len(caps) == 1) != (mode != "legacy") {
						t.Fatal("capabilities not negotiated")
					}
				case <-ctx.Done():
					t.Fatal("ready timeout")
				}
			}
			update := map[string]any{"v": 1, "event": "owner.name", "steam_id": owner.SteamID, "online_id": "Renamed"}
			switch mode {
			case "foreign-owner":
				update["steam_id"] = "76561198000000002"
			case "invalid-name":
				update["online_id"] = "bad\nname"
			case "extra-field":
				update["token"] = "not-allowed"
			}
			go json.NewEncoder(inW).Encode(update)
			if mode == "valid" {
				select {
				case <-applied:
				case <-ctx.Done():
					t.Fatal("name update not delivered")
				}
				if platform.Current().Username != "Renamed" || platform.Current().PID != 76561198000000001 {
					t.Fatal("owner identity changed")
				}
				// The first authentication may occur after a pipe rename. Its
				// original bootstrap token must still admit the current label.
				token, _ := json.Marshal(map[string]string{"id_token": owner.Token})
				extra, _ := json.Marshal(map[string]string{"token": string(token)})
				body, _ := json.Marshal(map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "1", "extra_data": string(extra)})
				id, err := platform.Authenticate(body)
				if err != nil || id.Username != "Renamed" || id.PID != platform.Current().PID {
					t.Fatal("rename before authentication changed admission")
				}
				select {
				case <-p.Done():
					t.Fatal("rename closed pipe")
				default:
				}
			} else {
				select {
				case <-p.Done():
				case <-ctx.Done():
					t.Fatal("unnegotiated or malformed name accepted")
				}
				if platform.Current().Username != "Initial" {
					t.Fatal("invalid update mutated owner")
				}
			}
		})
	}
}
