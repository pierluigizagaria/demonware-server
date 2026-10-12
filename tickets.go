package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PS4 tickets admit only a fresh connection, and the auth handler, lobby and
// Umbrella that read them run in this process, so they are kept in memory by
// the ticket's 8-byte session tag. A restart drops every lobby connection and
// clients authenticate again. The original admission keeps its files.
type issuedTicket struct {
	ticket []byte
	id     playerID
}

var (
	issuedMu      sync.Mutex
	issuedTickets = map[[8]byte]issuedTicket{}
)

// An issued identity stays readable this long after its ticket stops
// admitting, for a lobby that admitted it just before the window closed.
const issuedIdentityGrace = time.Minute

func ticketTag(ticket []byte) (tag [8]byte) {
	copy(tag[:], ticket[33:41])
	return tag
}

func ticketIssuedAt(ticket []byte) time.Time {
	return time.Unix(int64(binary.LittleEndian.Uint32(ticket[13:17])), 0)
}

// rememberTicket records a ticket issued to id and forgets those that can no
// longer be read, so the store holds at most one window of issuance.
func rememberTicket(ticket []byte, id playerID, now time.Time, window time.Duration) {
	issuedMu.Lock()
	defer issuedMu.Unlock()
	for tag, t := range issuedTickets {
		if now.Sub(ticketIssuedAt(t.ticket)) >= window+issuedIdentityGrace {
			delete(issuedTickets, tag)
		}
	}
	issuedTickets[ticketTag(ticket)] = issuedTicket{ticket: append([]byte(nil), ticket...), id: id}
}

// issuedTicketPlayer admits a ticket only if this server issued exactly these
// bytes within window and its identity matches the ticket's account.
func issuedTicketPlayer(ticket []byte, now time.Time, window time.Duration) (*playerID, bool) {
	if len(ticket) != 128 || binary.LittleEndian.Uint32(ticket) != ticketMagic {
		return nil, false
	}
	issued := ticketIssuedAt(ticket)
	if issued.After(now) || now.Sub(issued) >= window {
		return nil, false
	}
	return issuedIdentity(ticket)
}

// issuedIdentity returns the identity a ticket was issued to; it is not admission.
func issuedIdentity(ticket []byte) (*playerID, bool) {
	if len(ticket) != 128 {
		return nil, false
	}
	issuedMu.Lock()
	t, ok := issuedTickets[ticketTag(ticket)]
	issuedMu.Unlock()
	if !ok || !bytes.Equal(t.ticket, ticket) || t.id.PID == 0 || t.id.PID != binary.LittleEndian.Uint64(ticket[17:25]) {
		return nil, false
	}
	id := t.id
	id.Title = binary.LittleEndian.Uint32(ticket[5:9])
	return &id, true
}

// ticketWasIssued serves the original admission, whose tickets are named by
// issue time and are found by content.
func ticketWasIssued(dir string, ticket []byte) bool {
	files, _ := filepath.Glob(filepath.Join(dir, "*.tkt"))
	for _, file := range files {
		if stored, err := os.ReadFile(file); err == nil && bytes.Equal(stored, ticket) {
			return true
		}
	}
	return false
}

// storedIdentity reads the identity the original admission recorded at
// issuance; it is not admission.
func storedIdentity(dir string, ticket []byte) (*playerID, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("id_%x.json", ticket[33:41])))
	var id playerID
	if err != nil || json.Unmarshal(raw, &id) != nil || id.PID == 0 {
		return nil, false
	}
	id.Title = binary.LittleEndian.Uint32(ticket[5:9])
	return &id, true
}
