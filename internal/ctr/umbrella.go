package ctr

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

// The client encodes its integers as JSON strings, so accept both forms.
type jsonUint32 uint32

func (v *jsonUint32) UnmarshalJSON(b []byte) error {
	n, err := strconv.ParseUint(strings.Trim(string(b), `"`), 10, 32)
	if err != nil {
		return err
	}
	*v = jsonUint32(n)
	return nil
}

// Umbrella is a separate API from /auth/. CTR's bdUmbrellaUserAccount
// parser expects umbrellaID, accessToken, expires and accounts.
func HandleUmbrella(diag *diagnostics.Diagnostics, tickets *auth.TicketStore, w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
	deny := func(code int, reason string) {
		preview := body
		if len(preview) > 300 {
			preview = preview[:300]
		}
		diag.DiagnosticLogf("[ctr] umbrella #%d %s -> %d (%s) body=%s", n, r.URL.RequestURI(), code, reason, preview)
		http.Error(w, reason, code)
	}
	if !diag.RedactLogs && dumps != "" && len(body) > 0 {
		if err := os.WriteFile(filepath.Join(dumps, fmt.Sprintf("%03d_umbrella.json", n)), body, 0o644); err != nil {
			diag.DiagnosticLogf("[ctr] umbrella dump: %v", err)
		}
	}
	if r.Method != http.MethodPost {
		deny(405, "method not allowed")
		return
	}
	var req struct {
		Ticket  string     `json:"ticket"`
		TitleID jsonUint32 `json:"titleID"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		deny(400, fmt.Sprintf("unreadable request: %v", err))
		return
	}
	if !ps4.IsCTRTitle(uint32(req.TitleID)) {
		deny(400, fmt.Sprintf("unexpected title %d", req.TitleID))
		return
	}
	ticket, err := base64.StdEncoding.DecodeString(req.Ticket)
	if err != nil || len(ticket) != 128 || binary.LittleEndian.Uint32(ticket[:4]) != auth.TicketMagic || binary.LittleEndian.Uint32(ticket[5:9]) != uint32(req.TitleID) {
		deny(401, fmt.Sprintf("invalid ticket (decoded %d bytes, err %v)", len(ticket), err))
		return
	}
	// Accept only a ticket issued by this private server, not arbitrary claims.
	id, ok := tickets.IssuedTicketPlayer(ticket, time.Now(), ps4.TicketWindow)
	if !ok {
		deny(401, "ticket not issued here, too old or without identity")
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		deny(500, "token generation failed")
		return
	}
	diag.DiagnosticLogf("[ctr] umbrella #%d pid=%d -> 200 (lsg token issued)", n, id.PID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ID       uint64 `json:"umbrellaID"`
		Token    string `json:"accessToken"`
		Expires  int64  `json:"expires"`
		Accounts []any  `json:"accounts"`
	}{id.PID, base64.RawURLEncoding.EncodeToString(token), time.Now().Add(time.Hour).Unix(), []any{}})
}

func NewUmbrellaHandler(diag *diagnostics.Diagnostics, tickets *auth.TicketStore) auth.AuxiliaryAuthHandler {
	return func(w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
		HandleUmbrella(diag, tickets, w, r, body, n, dumps)
	}
}
