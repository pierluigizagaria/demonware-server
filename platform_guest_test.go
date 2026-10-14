package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func guestEntropy(ids ...uint64) *bytes.Reader {
	var b bytes.Buffer
	for _, id := range ids {
		_ = binary.Write(&b, binary.LittleEndian, id)
	}
	return bytes.NewReader(b.Bytes())
}

func TestGuestAdmissionIgnoresAccountClaims(t *testing.T) {
	p := guestPlatform{random: guestEntropy(0, 17, 29, 1<<63-1)}
	for _, token := range []string{"TOKEN", "", "not-a-signed-credential"} {
		req := psnAuthRequest(t, token)
		req["username"], req["account_id"] = "ClaimedName", "999"
		body, _ := json.Marshal(req)
		id, err := p.Authenticate(body)
		if err != nil || id.PID == 0 || id.PID == 999 || id.Username == "ClaimedName" || len(id.Username) > 16 || id.Kind != "guest" || id.Title != ctrTitle || !strings.HasPrefix(id.Sub, "guest:") {
			t.Fatal("guest depends on account claims or has invalid identity", err)
		}
		if !strings.HasPrefix(p.AuthData(id)["extended_data"], "nextendo:guest:") {
			t.Fatal("guest incorrectly labelled as a verified platform account")
		}
	}
}

func TestGuestAdmissionRefusesMalformedInputAndEntropyFailure(t *testing.T) {
	p := guestPlatform{random: iotest.ErrReader(errors.New("entropy unavailable"))}
	body, _ := json.Marshal(psnAuthRequest(t, "TOKEN"))
	if id, err := p.Authenticate(body); err == nil || id.PID != 0 {
		t.Fatal("entropy failure issued a guest")
	}
	for _, change := range []struct{ field, value string }{
		{"title_id", "5775"}, {"auth_task", "78"}, {"iv_seed", "4294967296"},
		{"extra_data", `{}`}, {"extra_data", `{"token":"{}"}`},
	} {
		req := psnAuthRequest(t, "TOKEN")
		req[change.field] = change.value
		body, _ = json.Marshal(req)
		if _, err := (guestPlatform{random: guestEntropy(17)}).Authenticate(body); err == nil {
			t.Fatal("malformed guest request accepted", change.field)
		}
	}
	for _, body := range [][]byte{
		[]byte(`{"title_id":"5760","title_id":"5760"}`),
		[]byte(strings.Repeat(" ", 16385)),
	} {
		if _, err := (guestPlatform{random: guestEntropy(17)}).Authenticate(body); err == nil {
			t.Fatal("ambiguous or oversized guest request accepted")
		}
	}
}

func TestGuestConfigIsExplicitAndNeedsNoShadNet(t *testing.T) {
	env := map[string]string{"CTR_ADMISSION": "guest", "CTR_UNLOCK_ALL": "1", "CTR_AUTH_SIGNING_KEY": "fixture.pem"}
	c, err := loadConfig(lookupFrom(env), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	p, namespace, err := loadPublisherPlatform(c)
	if err != nil || namespace != guestTrustID {
		t.Fatal("guest admission tried to load shadNet", err)
	}
	if _, ok := p.(guestPlatform); !ok {
		t.Fatal("wrong admission provider")
	}
	for _, tc := range []struct{ field, value string }{
		{"CTR_ADMISSION", "off"}, {"CTR_UNLOCK_ALL", "0"},
		{"SHADNET_ISSUER", "https://identity.example.net"}, {"CTR_AUTH_SIGNING_KEY", ""},
	} {
		t.Run(tc.field, func(t *testing.T) {
			prior := env[tc.field]
			env[tc.field] = tc.value
			defer func() { env[tc.field] = prior }()
			if _, err := loadConfig(lookupFrom(env), nil, true); err == nil {
				t.Fatal("invalid guest configuration accepted")
			}
		})
	}
	delete(env, "CTR_ADMISSION")
	if _, err := loadConfig(lookupFrom(env), nil, true); err == nil {
		t.Fatal("unlock-all silently disabled account authentication")
	}
}

func TestGuestNamespaceCannotReuseVerifiedAccountDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), ctrDB)
	db, err := openCTRSQLite(path, true, testTrustID)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if guest, err := openCTRSQLite(path, true, guestTrustID); err == nil {
		guest.Close()
		t.Fatal("guest instance reused verified account state")
	}
	path = filepath.Join(t.TempDir(), ctrDB)
	db, err = openCTRSQLite(path, true, guestTrustID)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if signed, err := openCTRSQLite(path, true, testTrustID); err == nil {
		signed.Close()
		t.Fatal("verified instance reused anonymous state")
	}
}

func TestGuestTicketsReachLobbyAndUmbrella(t *testing.T) {
	p := guestPlatform{random: guestEntropy(17, 29)}
	handler := testAuthHandler(p, testCTRSigner(t), "")
	body, _ := json.Marshal(psnAuthRequest(t, "TOKEN"))
	for _, want := range []uint64{17, 29} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/auth/", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:50000" // same address and body must not merge guests
		handler.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("X-Signature") == "" {
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
		id, ok := testTickets.issuedTicketPlayer(ticket, time.Now(), ticketWindow)
		if !ok || id.Kind != "guest" || id.PID != want {
			t.Fatal("guest ticket lost its identity")
		}
		l := handshake(t, ticket)
		if l.keys == nil || l.player == nil || l.player.Kind != "guest" || l.player.PID != want {
			t.Fatal("anonymous lobby handshake failed")
		}
		if nicknameOnline(want) != id.Username {
			t.Fatal("publisher name lookup lost the random nickname")
		}
		dropOnline(l.n)
		umbrella, _ := json.Marshal(map[string]any{"ticket": response.ServerTicket, "titleID": ctrTitle})
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
