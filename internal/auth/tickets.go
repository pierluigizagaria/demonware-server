package auth

import (
	bytes "bytes"
	binary "encoding/binary"
	platformpkg "github.com/pierluigizagaria/demonware-server/internal/platform"
	sync "sync"
	time "time"
)

// Tickets admit only a fresh connection, and the auth handler, lobby and
// Umbrella that read them run in this process, so they are kept in memory by
// the ticket's 8-byte session tag. A restart drops every lobby connection and
// clients authenticate again.
type issuedTicket struct {
	ticket []byte
	id     platformpkg.PlayerID
}

type TicketStore struct {
	Mu      sync.Mutex
	Entries map[[8]byte]issuedTicket
}

func NewTicketStore() *TicketStore { return &TicketStore{Entries: map[[8]byte]issuedTicket{}} }

// An issued identity stays readable this long after its ticket stops
// admitting, for a lobby that admitted it just before the window closed.
const IssuedIdentityGrace = time.Minute

func ticketTag(ticket []byte) (tag [8]byte) {
	copy(tag[:], ticket[33:41])
	return tag
}

func ticketIssuedAt(ticket []byte) time.Time {
	return time.Unix(int64(binary.LittleEndian.Uint32(ticket[13:17])), 0)
}

// rememberTicket records a ticket issued to id and forgets those that can no
// longer be read, so the store holds at most one window of issuance.
func (s *TicketStore) RememberTicket(ticket []byte, id platformpkg.PlayerID, now time.Time, window time.Duration) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for tag, t := range s.Entries {
		if now.Sub(ticketIssuedAt(t.ticket)) >= window+IssuedIdentityGrace {
			delete(s.Entries, tag)
		}
	}
	s.Entries[ticketTag(ticket)] = issuedTicket{ticket: append([]byte(nil), ticket...), id: id}
}

// issuedTicketPlayer admits a ticket only if this server issued exactly these
// bytes within window and its identity matches the ticket's account.
func (s *TicketStore) IssuedTicketPlayer(ticket []byte, now time.Time, window time.Duration) (*platformpkg.PlayerID, bool) {
	if len(ticket) != 128 || binary.LittleEndian.Uint32(ticket) != TicketMagic {
		return nil, false
	}
	issued := ticketIssuedAt(ticket)
	if issued.After(now) || now.Sub(issued) >= window {
		return nil, false
	}
	return s.IssuedIdentity(ticket)
}

// issuedIdentity returns the identity a ticket was issued to; it is not admission.
func (s *TicketStore) IssuedIdentity(ticket []byte) (*platformpkg.PlayerID, bool) {
	if len(ticket) != 128 {
		return nil, false
	}
	s.Mu.Lock()
	t, ok := s.Entries[ticketTag(ticket)]
	s.Mu.Unlock()
	if !ok || !bytes.Equal(t.ticket, ticket) || t.id.PID == 0 || t.id.PID != binary.LittleEndian.Uint64(ticket[17:25]) {
		return nil, false
	}
	id := t.id
	id.Title = binary.LittleEndian.Uint32(ticket[5:9])
	return &id, true
}
