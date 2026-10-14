package main

import (
	"bytes"
	"encoding/binary"
	"sync"
	"time"
)

// Tickets admit only a fresh connection, and the auth handler, lobby and
// Umbrella that read them run in this process, so they are kept in memory by
// the ticket's 8-byte session tag. A restart drops every lobby connection and
// clients authenticate again.
type issuedTicket struct {
	ticket []byte
	id     playerID
}

type ticketStore struct {
	mu      sync.Mutex
	entries map[[8]byte]issuedTicket
}

func newTicketStore() *ticketStore { return &ticketStore{entries: map[[8]byte]issuedTicket{}} }

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
func (s *ticketStore) rememberTicket(ticket []byte, id playerID, now time.Time, window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tag, t := range s.entries {
		if now.Sub(ticketIssuedAt(t.ticket)) >= window+issuedIdentityGrace {
			delete(s.entries, tag)
		}
	}
	s.entries[ticketTag(ticket)] = issuedTicket{ticket: append([]byte(nil), ticket...), id: id}
}

// issuedTicketPlayer admits a ticket only if this server issued exactly these
// bytes within window and its identity matches the ticket's account.
func (s *ticketStore) issuedTicketPlayer(ticket []byte, now time.Time, window time.Duration) (*playerID, bool) {
	if len(ticket) != 128 || binary.LittleEndian.Uint32(ticket) != ticketMagic {
		return nil, false
	}
	issued := ticketIssuedAt(ticket)
	if issued.After(now) || now.Sub(issued) >= window {
		return nil, false
	}
	return s.issuedIdentity(ticket)
}

// issuedIdentity returns the identity a ticket was issued to; it is not admission.
func (s *ticketStore) issuedIdentity(ticket []byte) (*playerID, bool) {
	if len(ticket) != 128 {
		return nil, false
	}
	s.mu.Lock()
	t, ok := s.entries[ticketTag(ticket)]
	s.mu.Unlock()
	if !ok || !bytes.Equal(t.ticket, ticket) || t.id.PID == 0 || t.id.PID != binary.LittleEndian.Uint64(ticket[17:25]) {
		return nil, false
	}
	id := t.id
	id.Title = binary.LittleEndian.Uint32(ticket[5:9])
	return &id, true
}
