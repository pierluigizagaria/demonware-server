package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/iotest"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func testCTRSigner(t *testing.T) *CTRSigner {
	t.Helper()
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl unavailable")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signer.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadCTRSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fixturePSNPlatform() ps4.PSNPlatform {
	return ps4.PSNPlatform{Verifier: platformVerifierFunc(func(string, time.Time) (platform.Identity, error) {
		return platform.Identity{AccountID: 17, Subject: "17", OnlineID: "fixture"}, nil
	})}
}

func psnTicket(issued time.Time) []byte {
	ticket := make([]byte, 128)
	binary.LittleEndian.PutUint32(ticket, TicketMagic)
	ticket[4] = 1
	binary.LittleEndian.PutUint32(ticket[5:], 5760)
	binary.LittleEndian.PutUint32(ticket[13:], uint32(issued.Unix()))
	binary.LittleEndian.PutUint64(ticket[17:], 17)
	binary.LittleEndian.PutUint64(ticket[25:], 17)
	_, _ = rand.Read(ticket[33:97])
	copy(ticket[97:121], ticket[33:57])
	return ticket
}

func TestPS4AuthRequiresResponseSigner(t *testing.T) {
	testTickets := NewTicketStore()
	h := testAuthHandler(testTickets, fixturePSNPlatform(), nil, "")
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	before := len(testTickets.Entries)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
	if w.Code == 200 {
		t.Fatal("PS4 auth succeeded without required response signature")
	}
	if len(testTickets.Entries) != before {
		t.Fatal("ticket issued without a signed response")
	}
}

func TestAuthRefusesIncompleteBody(t *testing.T) {
	testTickets := NewTicketStore()
	h := testAuthHandler(testTickets, fixturePSNPlatform(), testCTRSigner(t), "")
	before := len(testTickets.Entries)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", iotest.ErrReader(io.ErrUnexpectedEOF)))
	if w.Code != 400 {
		t.Fatalf("incomplete body answered %d", w.Code)
	}
	if len(testTickets.Entries) != before {
		t.Fatal("ticket issued for an incomplete request")
	}
}
