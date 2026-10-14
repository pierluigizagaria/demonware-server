package main

// Demonware authentication for Diablo III Switch (title "crimson").
//
// The client (bdAuthSwitch, transport bdHTTPCurl) hits
//
//	https://crimson-switch-auth3.prod.demonware.net:<port>/auth/
//
// via sni-router, then switches to the lobby (lobby.go). The game's NSO gives
// the hosts, the URL template "https://%s:%d/auth/" and the ticket vocabulary;
// the body format comes from captures and the parser at 0xBE30C0.

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// bdAuth ticket magic, read from the NSO (rodata 0xF07FB8): bytes DE AD BD EF.
const ticketMagic uint32 = 0xEFBDADDE

func serveAuth(platform publisherPlatform, tickets *ticketStore, presence *presenceStore, cfg serverConfig, diag *diagnostics, monitor *telemetry, umbrella auxiliaryAuthHandler) error {
	ctrSigner, err := loadCTRAuthSigner(cfg.signingKey)
	if err != nil {
		return err
	}
	if ctrSigner == nil {
		return fmt.Errorf("CTR_AUTH_SIGNING_KEY is required")
	}
	diag.diagnosticLogf("[CTR Auth] signed replies enabled (client trust mod required)")
	dumps := diag.runDumpDir("auth")

	h := newPlatformAuthHandler(diag, monitor, tickets, presence, platform, ctrSigner, dumps, umbrella)

	srv := &http.Server{
		Addr:    net.JoinHostPort(cfg.bindAddr, strconv.Itoa(cfg.authPort)),
		Handler: h,
		// The client is an embedded 2018 curl: don't require TLS 1.2+.
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS10},
		ReadHeaderTimeout: 15 * time.Second,
		// Bounds reading one request, body included. Idle keep-alive stays
		// unlimited as before (a zero IdleTimeout would inherit ReadTimeout).
		ReadTimeout: 30 * time.Second,
		IdleTimeout: -1,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	// Behind sni-router: the PROXY header carries the player's address (proxyproto.go).
	if cfg.proxyProtocol {
		ln = newProxyListener(ln)
	}
	diag.diagnosticLogf("[D3 Auth] listening HTTPS :%d (proxyProto=%v, cert=%s)", cfg.authPort, cfg.proxyProtocol, cfg.certFile)
	return srv.ServeTLS(ln, cfg.certFile, cfg.keyFile)
}

func newPlatformAuthHandler(diag *diagnostics, monitor *telemetry, tickets *ticketStore, presence *presenceStore, platform publisherPlatform, ctrSigner *ctrAuthSigner, dumps string, umbrella auxiliaryAuthHandler) http.Handler {
	var authReqNum atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := authReqNum.Add(1)
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body.Close()
		if err != nil {
			diag.diagnosticLogf("[D3 Auth] #%d body: %v", n, err)
			http.Error(w, "incomplete request", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/v1.0/tokens/lsg/" {
			umbrella(w, r, body, n, dumps)
			return
		}

		// Request line only: the body carries the player's NSA id_token.
		diag.diagnosticLogf("[D3 Auth] #%d %s %s%s from %s (%d bytes)", n, r.Method, r.Host, r.URL.RequestURI(), r.RemoteAddr, len(body))
		if !diag.redactLogs && dumps != "" && len(body) > 0 {
			name := fmt.Sprintf("%03d_%s_%s.bin", n, r.Method,
				strings.NewReplacer("/", "_", "?", "_", "&", "_", ":", "_").Replace(strings.TrimPrefix(r.URL.RequestURI(), "/")))
			if err := os.WriteFile(filepath.Join(dumps, name), body, 0o644); err != nil {
				diag.diagnosticLogf("[D3 Auth] dump: %v", err)
			}
		}

		// --- auth response ---------------------------------------------
		// Shape deduced from the parser in the NSO (0xBE30C0):
		//   code          must be 700 (0x2BC), otherwise the task fails
		//   iv_seed       integer, echoed back by the client
		//   client_ticket base64 -> exactly 128 bytes
		//   server_ticket base64 -> exactly 128 bytes, copied verbatim into
		//                 the auth object and passed on to the lobby. Opaque
		//                 to the client: only our lobby cares about its content.
		var req struct {
			AuthTask string `json:"auth_task"`
			IVSeed   string `json:"iv_seed"`
			TitleID  string `json:"title_id"`
			Identity string `json:"identity"`
		}
		_ = json.Unmarshal(body, &req)

		// The instance's admission provider supplies identity before ticket issuance.
		id, identityErr := platform.Authenticate(body)
		if identityErr != nil {
			reason := "platform verification failed"
			diag.diagnosticLogf("[D3 Auth] #%d %q pid=%d kind=%s REFUSED (%s)", n, id.Username, id.PID, id.Kind, reason)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if ctrSigner == nil {
			diag.diagnosticLogf("[D3 Auth] #%d refused: responses must be signed", n)
			http.Error(w, "authentication signing unavailable", http.StatusInternalServerError)
			return
		}

		// The client reads the ticket's first 4 bytes and compares them to the
		// magic 0xEFBDADDE (bytes DE AD BD EF), resolved via an
		// R_AARCH64_RELATIVE relocation to 0xF07FB8. If they match, the
		// decryption branch is skipped and the ticket is read in the clear —
		// this is what makes a standalone server possible with no key and no
		// client patch.
		//
		// Layout read from parse_ticket (0xBFCF30), 128 bytes packed:
		//   +0 u32 magic | +4 u8 | +5 u32 | +9 u32 | +13 u32
		//   +17 u64 | +25 u64 | +33 [64] session key | +97 [24] | +121 [3] | +124 [4]
		clientTicket := make([]byte, 128)
		binary.LittleEndian.PutUint32(clientTicket[0:], ticketMagic)
		clientTicket[4] = 1
		// Echo the title the client announced (Diablo III 5745, Crash Team Racing
		// 5775) instead of assuming one: a ticket stamped with another game's title
		// is not the ticket this client asked for.
		titleID := uint32(5745)
		if v, err := strconv.ParseUint(req.TitleID, 10, 32); err == nil && v != 0 {
			titleID = uint32(v)
		}
		if id.Title != 0 {
			titleID = id.Title // verified by the platform, not just announced
		}
		binary.LittleEndian.PutUint32(clientTicket[5:], titleID)
		binary.LittleEndian.PutUint32(clientTicket[9:], 0) // reserve
		binary.LittleEndian.PutUint32(clientTicket[13:], uint32(time.Now().Unix()))
		// CTR reads its online id at +25, where Diablo keeps the expiry.
		// Verified identities stay stable: the game ties a match to its host through
		// CFriendManager::getFriendByOnlineId(Net::MatchMakingInfo +0x170), and an
		// id that changes on every connection matches no friend. Guest admission
		// intentionally has no platform friend association.
		userID, expiry := uint64(1), uint64(time.Now().Unix()+86400)
		if isCTRTitle(titleID) {
			userID, expiry = id.PID, id.PID
		}
		binary.LittleEndian.PutUint64(clientTicket[17:], userID)
		binary.LittleEndian.PutUint64(clientTicket[25:], expiry)
		if _, err := rand.Read(clientTicket[33:97]); err != nil { // session key
			diag.diagnosticLogf("[D3 Auth] rand: %v", err)
		}
		// +97 [24]: the lobby key. The lobby connection (0xBFC8F0) copies a
		// config block whose bytes 0x88..0xA0 become conn+0x100, the key that
		// signs the handshake. It comes from the parsed ticket's 24-byte
		// field; we put the session key's first 24 bytes there so both
		// possible reads land on the same value.
		copy(clientTicket[97:121], clientTicket[33:57])

		// Opaque to the client: it copies this back verbatim and passes it to
		// the lobby in the 0x82. Our lobby is the one that reads it back, so
		// we put everything in it.
		serverTicket := append([]byte{}, clientTicket...)

		// The lobby finds the ticket echoed in the 0x82 by its 8-byte tag and
		// takes the session key and the player's identity from it (tickets.go).
		tickets.rememberTicket(clientTicket, id, time.Now(), ticketWindow)
		// 12/10 resolves friends by the names they were issued a ticket under.
		if err := presence.rememberPlayerName(id, time.Now()); err != nil {
			diag.diagnosticLogf("[D3 Auth] #%d player name not recorded: %v", n, err)
		}

		ivSeed := req.IVSeed
		if ivSeed == "" {
			ivSeed = "0"
		}

		// auth_task is read BEFORE code and validated by vtable[0x40]: without
		// it the response is rejected before code is even looked at (735 back).
		// This is NOT an echo: bdAuth pairs request and response as n -> n+1
		// (the console sends 78 and expects 79, seen via a d3hack hook).
		authTask := "79"
		if v, err := strconv.Atoi(req.AuthTask); err == nil {
			authTask = strconv.Itoa(v + 1)
		}

		// The client encodes ALL its integers as JSON strings ("auth_task":
		// "78"): we reply in the same style. ORDER matters (a sequential
		// parser: auth_task, code, iv_seed, client_ticket, server_ticket),
		// hence a struct and not a map.
		//
		// extra_data is a STRING holding JSON. vtable[0x50] (0xBE1440) reparses it and reads
		// nso_subscription_status as a u16 (a successful read makes it return true, so no game
		// patch and no real NSO); bdAuthSwitch::processPlatformData also reads extended_data.
		// Without extended_data (non-empty, <= 4096 chars) bdExtendedAuthInfo stays empty,
		// bdAntiCheat::reportExtendedAuthInfo gives up and igNetTaskReportExtendedAuthInfo
		// fails, which fails the WHOLE LSG login sequence: the game retries with backoff, then
		// goes offline until restart.
		extra, _ := json.Marshal(platform.AuthData(id))

		resp := struct {
			AuthTask     string `json:"auth_task"`
			Code         string `json:"code"`
			IVSeed       string `json:"iv_seed"`
			ClientTicket string `json:"client_ticket"`
			ServerTicket string `json:"server_ticket"`
			ExtraData    string `json:"extra_data"`
		}{
			AuthTask:     authTask,
			Code:         "700",
			IVSeed:       ivSeed,
			ClientTicket: base64.StdEncoding.EncodeToString(clientTicket),
			ServerTicket: base64.StdEncoding.EncodeToString(serverTicket),
			ExtraData:    string(extra),
		}
		out, _ := json.Marshal(resp)
		if isCTRTitle(titleID) && ctrSigner != nil {
			signature, err := ctrSigner.sign(out)
			if err != nil {
				diag.diagnosticLogf("[CTR Auth] %v", err)
				http.Error(w, "authentication signing failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("X-Signature", signature)
		}

		monitor.loginsTotal.Add(1)
		diag.diagnosticLogf("[D3 Auth] #%d %q pid=%d kind=%s -> 200 code=700 auth_task=%s", n, id.Username, id.PID, id.Kind, authTask)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	})

}

type auxiliaryAuthHandler func(http.ResponseWriter, *http.Request, []byte, uint64, string)
