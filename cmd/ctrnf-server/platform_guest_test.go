package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/platform/guest"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func guestEntropy(ids ...uint64) *bytes.Reader {
	var b bytes.Buffer
	for _, id := range ids {
		_ = binary.Write(&b, binary.LittleEndian, id)
	}
	return bytes.NewReader(b.Bytes())
}

func TestGuestConfigIsExplicitAndNeedsNoShadNet(t *testing.T) {
	env := map[string]string{"CTR_ADMISSION": "guest", "CTR_UNLOCK_ALL": "1"}
	c, err := config.Load(lookupFrom(env), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	p, namespace, err := loadPublisherPlatform(c)
	if err != nil || namespace != guest.TrustID {
		t.Fatal("guest admission tried to load shadNet", err)
	}
	if _, ok := p.(guest.Platform); !ok {
		t.Fatal("wrong admission provider")
	}
	for _, tc := range []struct{ field, value string }{
		{"CTR_ADMISSION", "off"}, {"CTR_UNLOCK_ALL", "0"},
		{"SHADNET_ISSUER", "https://identity.example.net"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			prior := env[tc.field]
			env[tc.field] = tc.value
			defer func() { env[tc.field] = prior }()
			if _, err := config.Load(lookupFrom(env), nil, true); err == nil {
				t.Fatal("invalid guest configuration accepted")
			}
		})
	}
	delete(env, "CTR_ADMISSION")
	if _, err := config.Load(lookupFrom(env), nil, true); err == nil {
		t.Fatal("unlock-all silently disabled account authentication")
	}
}

func TestGuestTicketsReachLobbyAndUmbrella(t *testing.T) {
	p := guest.Platform{Random: guestEntropy(17, 29)}
	handler := testAuthHandler(p, "")
	body, _ := json.Marshal(psnAuthRequest(t, "TOKEN"))
	for _, want := range []uint64{17, 29} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/auth/", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:50000" // same address and body must not merge guests
		handler.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("X-Signature") != "" {
			t.Fatal("anonymous admission failed", w.Code)
		}
		var response struct {
			ServerTicket string `json:"server_ticket"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		ticket, err := base64.StdEncoding.DecodeString(response.ServerTicket)
		if err != nil || len(ticket) != 128 || binary.LittleEndian.Uint64(ticket[17:25]) != want || binary.LittleEndian.Uint64(ticket[25:33]) != want {
			t.Fatal("guest ticket IDs disagree")
		}
		id, ok := testTickets.IssuedTicketPlayer(ticket, time.Now(), ps4.TicketWindow)
		if !ok || id.Kind != "guest" || id.PID != want {
			t.Fatal("guest ticket lost its identity")
		}
		l := handshake(t, ticket)
		if !l.ChannelSnapshot().Established || l.Player == nil || l.Player.Kind != "guest" || l.Player.PID != want {
			t.Fatal("anonymous lobby handshake failed")
		}
		if testPresence.NicknameOnline(want) != id.Username {
			t.Fatal("publisher name lookup lost the random nickname")
		}
		testPresence.DropOnline(l.N)
		umbrella, _ := json.Marshal(map[string]any{"ticket": response.ServerTicket, "titleID": ps4.CTRTitle})
		uw := httptest.NewRecorder()
		handler.ServeHTTP(uw, httptest.NewRequest("POST", "/v1.0/tokens/lsg/", bytes.NewReader(umbrella)))
		var result struct {
			ID uint64 `json:"umbrellaID"`
		}
		if uw.Code != 200 || json.Unmarshal(uw.Body.Bytes(), &result) != nil || result.ID != want {
			t.Fatal("Umbrella did not preserve the guest identity")
		}
	}
}
