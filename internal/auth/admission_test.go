package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"testing/iotest"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

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

func TestPS4AuthReplyIsUnsigned(t *testing.T) {
	testTickets := NewTicketStore()
	h := testAuthHandler(testTickets, fixturePSNPlatform(), "")
	body, _ := json.Marshal(psnAuthRequest(t, "synthetic-fixture"))
	before := len(testTickets.entries)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", bytes.NewReader(body)))
	if w.Code != 200 || len(testTickets.entries) != before+1 {
		t.Fatal("verified PS4 auth did not issue its ticket")
	}
	if _, signed := w.Header()["X-Signature"]; signed {
		t.Fatal("reply carries a response signature")
	}
}

func TestAuthRefusesIncompleteBody(t *testing.T) {
	testTickets := NewTicketStore()
	h := testAuthHandler(testTickets, fixturePSNPlatform(), "")
	before := len(testTickets.entries)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/", iotest.ErrReader(io.ErrUnexpectedEOF)))
	if w.Code != 400 {
		t.Fatalf("incomplete body answered %d", w.Code)
	}
	if len(testTickets.entries) != before {
		t.Fatal("ticket issued for an incomplete request")
	}
}
