package steam_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/platform/steam"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

func TestLiveSteamAdmission(t *testing.T) {
	helperPath, clientPath, evidence := os.Getenv("OSW_LIVE_HELPER"), os.Getenv("OSW_LIVE_CLIENT"), os.Getenv("OSW_LIVE_DIR")
	if helperPath == "" || clientPath == "" || evidence == "" {
		t.Skip("explicit live Steam executables and private evidence directory required")
	}
	dir, err := os.MkdirTemp(evidence, "publisher-admission-")
	if err != nil {
		t.Fatal(err)
	}
	diagnosticLog, err := os.Create(filepath.Join(dir, "publisher.log"))
	if err != nil {
		t.Fatal("cannot create private diagnostic log")
	}
	previousLog := log.Writer()
	log.SetOutput(diagnosticLog)
	defer func() { log.SetOutput(previousLog); diagnosticLog.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	helper, err := steam.StartHelper(ctx, helperPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer helper.Close()
	db, err := store.OpenSQLite(filepath.Join(dir, store.DBFile), false, "ctrnf:steam:480:5760:v1:local-test")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tokenPath, stopPath := filepath.Join(dir, "ticket.txt"), filepath.Join(dir, "client.stop")
	log, err := os.Create(filepath.Join(dir, "client.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	client := exec.Command(clientPath, "--bind-server", strconv.FormatUint(helper.ServerID(), 10), "--token", tokenPath, "--stop-file", stopPath)
	client.Stdout, client.Stderr = log, log
	if err := client.Start(); err != nil {
		t.Fatal("cannot start live ticket issuer")
	}
	done := make(chan error, 1)
	go func() { done <- client.Wait() }()
	defer func() {
		_ = os.WriteFile(stopPath, nil, 0600)
		select {
		case err := <-done:
			if err != nil {
				t.Error("issuer did not stop cleanly")
			}
		case <-time.After(20 * time.Second):
			_ = client.Process.Kill()
			<-done
			t.Error("issuer stop timed out")
		}
	}()
	deadline := time.Now().Add(35 * time.Second)
	var credentialBytes []byte
	for time.Now().Before(deadline) {
		credentialBytes, err = os.ReadFile(tokenPath)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("no live session ticket produced")
	}
	value := strings.TrimSpace(string(credentialBytes))
	token, err := steam.ParseToken(value)
	if err != nil {
		t.Fatal("invalid live token")
	}
	inner, _ := json.Marshal(map[string]string{"id_token": value})
	extra, _ := json.Marshal(map[string]string{"token": string(inner)})
	body, _ := json.Marshal(map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "1", "extra_data": string(extra)})
	provider := &steam.Platform{Validator: helper, Store: db, Realm: "local-test"}
	tickets := auth.NewTicketStore()
	handler := auth.NewPlatformAuthHandler(&diagnostics.Diagnostics{RedactLogs: true}, dashboard.NewTelemetry(config.Config{}, func() int64 { return 0 }), tickets, presence.NewStore(nil), provider, "", nil)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	httpClient := server.Client()
	httpClient.Timeout = 35 * time.Second
	discovery, err := httpClient.Get(server.URL + "/orbi/steam/v1")
	if err != nil {
		t.Fatal("TLS discovery request failed")
	}
	var endpoint map[string]string
	err = json.NewDecoder(discovery.Body).Decode(&endpoint)
	discovery.Body.Close()
	if err != nil || discovery.StatusCode != http.StatusOK || discovery.Header.Get("Cache-Control") != "no-store" || endpoint["server_id"] != strconv.FormatUint(helper.ServerID(), 10) || endpoint["realm"] != "local-test" || endpoint["app_id"] != "480" {
		t.Fatal("TLS discovery contract failed")
	}
	response, err := httpClient.Post(server.URL+"/auth/", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal("TLS publisher admission request failed")
	}
	var envelope map[string]string
	err = json.NewDecoder(response.Body).Decode(&envelope)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("X-Signature") != "" || envelope["code"] != "700" || envelope["auth_task"] != "47" {
		t.Fatal("HTTPS admission failed")
	}
	publisherTicket, err := base64.StdEncoding.DecodeString(envelope["server_ticket"])
	if err != nil {
		t.Fatal("publisher ticket decoding failed")
	}
	id, admitted := tickets.IssuedTicketPlayer(publisherTicket, time.Now(), ps4.TicketWindow)
	if !admitted || id.PID != token.AccountID || id.Kind != "steam" {
		t.Fatal("HTTPS ticket handoff lost verified identity")
	}
	defer id.Admission.Close()
	if !id.Admission.Claim() {
		t.Fatal("cannot hand admission to connection")
	}
	id.Admission.Expire()
	if _, err := provider.Authenticate(body); err == nil {
		t.Fatal("live credential replay admitted")
	}
	id.Admission.Close()
	if _, err := provider.Authenticate(body); err == nil {
		t.Fatal("credential replay admitted after connection end")
	}
	used, err := db.SteamTicketUsed(token.Hash)
	if err != nil || !used {
		t.Fatal("consumed credential missing from persistent store")
	}
	result := map[string]any{"verified_admission": true, "identity_matches_issuer": true, "bound_ticket": true,
		"replay_refused_active": true, "replay_refused_after_end": true, "persistent_hash": true,
		"https_discovery": true, "https_signed_auth": true, "publisher_ticket_handoff": true,
		"game_lobby_wire_tested": false}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "result.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("Live native-helper / publisher / SQLite acceptance passed; private evidence:", dir)
}
