package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/platform/local"
)

func TestWeakNativeAuthUsesSocketSourceAndInheritedIdentity(t *testing.T) {
	owner := local.Owner{SteamID: "76561198000000001", OnlineID: "Trusted.Name", Token: "osw1.76561198000000001." + strings.Repeat("ab", 32) + "." + base64.RawURLEncoding.EncodeToString([]byte("Trusted.Name")), Title: "5760", Build: "01.21", Realm: "test", Backend: "lab"}
	p, err := local.NewWithWeakToken(owner, true)
	if err != nil {
		t.Fatal(err)
	}
	tickets := NewTicketStore()
	handler := testAuthHandler(tickets, p, "")
	for _, test := range []struct {
		remote  string
		account uint64
		status  int
	}{
		{"127.0.0.1:3210", p.PID(), http.StatusOK},
		{"127.0.0.1:3210", p.PID() + 1, http.StatusForbidden},
		{"192.0.2.1:3210", p.PID(), http.StatusForbidden},
		{"", p.PID(), http.StatusForbidden},
	} {
		token, _ := json.Marshal(map[string]string{"id_token": fmt.Sprintf("sacredfire.Other.Name !.%016x", test.account)})
		extra, _ := json.Marshal(map[string]string{"token": string(token)})
		body, _ := json.Marshal(map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "1", "extra_data": string(extra)})
		request := httptest.NewRequest(http.MethodPost, "/auth/", bytes.NewReader(body))
		request.RemoteAddr = test.remote
		request.Header.Set("X-Forwarded-For", "127.0.0.1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatal("weak admission ignored actual socket identity")
		}
		if test.status == http.StatusOK {
			var answer map[string]string
			if json.Unmarshal(response.Body.Bytes(), &answer) != nil {
				t.Fatal("invalid auth reply")
			}
			raw, err := base64.StdEncoding.DecodeString(answer["server_ticket"])
			if err != nil || len(raw) != 128 || binary.LittleEndian.Uint64(raw[17:25]) != p.PID() {
				t.Fatal("ticket identity differs from pipe owner")
			}
			id, ok := tickets.IssuedIdentity(raw)
			if !ok || id.Username != owner.OnlineID || response.Header().Get("X-Signature") != "" {
				t.Fatal("weak token name replaced trusted identity or unsigned response")
			}
		}
	}
}
