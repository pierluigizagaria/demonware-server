package guest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func guestEntropy(ids ...uint64) *bytes.Reader {
	var b bytes.Buffer
	for _, id := range ids {
		_ = binary.Write(&b, binary.LittleEndian, id)
	}
	return bytes.NewReader(b.Bytes())
}

func TestGuestAdmissionIgnoresAccountClaims(t *testing.T) {
	p := Platform{Random: guestEntropy(0, 17, 29, 1<<63-1)}
	for _, token := range []string{"TOKEN", "", "not-a-signed-credential"} {
		req := psnAuthRequest(t, token)
		req["username"], req["account_id"] = "ClaimedName", "999"
		body, _ := json.Marshal(req)
		id, err := p.Authenticate(body)
		if err != nil || id.PID == 0 || id.PID == 999 || id.Username == "ClaimedName" || len(id.Username) > 16 || id.Kind != "guest" || id.Title != ps4.CTRTitle || !strings.HasPrefix(id.Sub, "guest:") {
			t.Fatal("guest depends on account claims or has invalid identity", err)
		}
		if !strings.HasPrefix(p.AuthData(id)["extended_data"], "nextendo:guest:") {
			t.Fatal("guest incorrectly labelled as a verified platform account")
		}
	}
}

func TestGuestAdmissionRefusesMalformedInputAndEntropyFailure(t *testing.T) {
	p := Platform{Random: iotest.ErrReader(errors.New("entropy unavailable"))}
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
		if _, err := (Platform{Random: guestEntropy(17)}).Authenticate(body); err == nil {
			t.Fatal("malformed guest request accepted", change.field)
		}
	}
	for _, body := range [][]byte{
		[]byte(`{"title_id":"5760","title_id":"5760"}`),
		[]byte(strings.Repeat(" ", 16385)),
	} {
		if _, err := (Platform{Random: guestEntropy(17)}).Authenticate(body); err == nil {
			t.Fatal("ambiguous or oversized guest request accepted")
		}
	}
}

func psnAuthRequest(t *testing.T, token string) map[string]string {
	t.Helper()
	envelope, err := json.Marshal(map[string]string{"id_token": token})
	if err != nil {
		t.Fatal(err)
	}
	extra, err := json.Marshal(map[string]string{"token": string(envelope), "username": "untrusted"})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "4294967295", "extra_data": string(extra)}
}
