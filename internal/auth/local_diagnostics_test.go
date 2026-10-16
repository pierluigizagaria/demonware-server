package auth

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/local"
)

type refusalPlatform struct{ err error }

func (p refusalPlatform) Authenticate([]byte) (platform.PlayerID, error) {
	return platform.PlayerID{}, p.err
}
func (refusalPlatform) AuthData(platform.PlayerID) map[string]string { return nil }

func TestLocalAuthRefusalLogsOnlyFixedCategory(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, test := range []struct {
		category local.AdmissionError
		label    string
	}{
		{local.MalformedEnvelope, "malformed-envelope"},
		{local.UnsupportedTask, "unsupported-task"},
		{local.OwnerCapabilityMismatch, "owner-capability-mismatch"},
	} {
		output.Reset()
		diag := &diagnostics.Diagnostics{RedactLogs: true, PrivateIdentity: true}
		h := NewPlatformAuthHandler(diag, nil, nil, nil, refusalPlatform{test.category}, nil, "", nil)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/auth/", strings.NewReader(`{"auth_task":"private-input","identity":"private-identity"}`)))
		if response.Code != http.StatusForbidden || !strings.Contains(output.String(), "local refusal: "+test.label) || strings.Contains(output.String(), "private-") {
			t.Fatal("refusal changed status or disclosed input")
		}
	}
}
