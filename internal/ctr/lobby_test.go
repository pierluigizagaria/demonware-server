package ctr

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func TestCTRUmbrellaIssuedTicket(t *testing.T) {
	tickets := auth.NewTicketStore()
	testUmbrellaHandler := NewUmbrellaHandler(&diagnostics.Diagnostics{RedactLogs: true}, tickets)
	issueTestTicket := func(ticket []byte, id platform.PlayerID) {
		tickets.RememberTicket(ticket, id, time.Now(), ps4.TicketWindow)
	}

	ticket := psnTicket(time.Now())
	call := func(titleID any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"ticket": base64.StdEncoding.EncodeToString(ticket), "titleID": titleID})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1.0/tokens/lsg/", bytes.NewReader(body))
		testUmbrellaHandler(w, r, body, 1, "")
		return w
	}
	if w := call(5760); w.Code != 401 {
		t.Fatalf("unissued ticket accepted: %d", w.Code)
	}
	issueTestTicket(ticket, platform.PlayerID{PID: 17, Username: "fixture"})
	// The client sends its integers as JSON strings, so both forms must pass.
	if w := call("5760"); w.Code != 200 {
		t.Fatalf("string-encoded titleID rejected: %d %s", w.Code, w.Body)
	}
	w := call(5760)
	if w.Code != 200 {
		t.Fatalf("issued ticket rejected: %d", w.Code)
	}
	var result struct {
		ID       uint64 `json:"umbrellaID"`
		Token    string `json:"accessToken"`
		Expires  int64  `json:"expires"`
		Accounts []any  `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != 17 || result.Token == "" || result.Expires <= time.Now().Unix() || result.Accounts == nil {
		t.Fatal("incomplete CTR account response")
	}
}
