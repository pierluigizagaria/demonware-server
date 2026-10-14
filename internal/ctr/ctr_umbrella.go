package ctr

import (
	rand "crypto/rand"
	base64 "encoding/base64"
	binary "encoding/binary"
	json "encoding/json"
	fmt "fmt"
	authpkg "github.com/NextendoNetwork/nextendo-demonware/internal/auth"
	diagnosticspkg "github.com/NextendoNetwork/nextendo-demonware/internal/diagnostics"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	http "net/http"
	os "os"
	filepath "path/filepath"
	strconv "strconv"
	strings "strings"
	time "time"
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
func HandleCTRUmbrella(diag *diagnosticspkg.Diagnostics, tickets *authpkg.TicketStore, w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
	deny := func(code int, reason string) {
		preview := body
		if len(preview) > 300 {
			preview = preview[:300]
		}
		diag.DiagnosticLogf("[CTR Umbrella] #%d %s -> %d (%s) body=%s", n, r.URL.RequestURI(), code, reason, preview)
		http.Error(w, reason, code)
	}
	if !diag.RedactLogs && dumps != "" && len(body) > 0 {
		if err := os.WriteFile(filepath.Join(dumps, fmt.Sprintf("%03d_umbrella.json", n)), body, 0o644); err != nil {
			diag.DiagnosticLogf("[CTR Umbrella] dump: %v", err)
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
	if !ps4pkg.IsCTRTitle(uint32(req.TitleID)) {
		deny(400, fmt.Sprintf("unexpected title %d", req.TitleID))
		return
	}
	ticket, err := base64.StdEncoding.DecodeString(req.Ticket)
	if err != nil || len(ticket) != 128 || binary.LittleEndian.Uint32(ticket[:4]) != authpkg.TicketMagic || binary.LittleEndian.Uint32(ticket[5:9]) != uint32(req.TitleID) {
		deny(401, fmt.Sprintf("invalid ticket (decoded %d bytes, err %v)", len(ticket), err))
		return
	}
	// Accept only a ticket issued by this private server, not arbitrary claims.
	id, ok := tickets.IssuedTicketPlayer(ticket, time.Now(), ps4pkg.TicketWindow)
	if !ok {
		deny(401, "ticket not issued here, too old or without identity")
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		deny(500, "token generation failed")
		return
	}
	diag.DiagnosticLogf("[CTR Umbrella] #%d pid=%d -> 200 (lsg token issued)", n, id.PID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ID       uint64 `json:"umbrellaID"`
		Token    string `json:"accessToken"`
		Expires  int64  `json:"expires"`
		Accounts []any  `json:"accounts"`
	}{id.PID, base64.RawURLEncoding.EncodeToString(token), time.Now().Add(time.Hour).Unix(), []any{}})
}

func NewUmbrellaHandler(diag *diagnosticspkg.Diagnostics, tickets *authpkg.TicketStore) authpkg.AuxiliaryAuthHandler {
	return func(w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
		HandleCTRUmbrella(diag, tickets, w, r, body, n, dumps)
	}
}
