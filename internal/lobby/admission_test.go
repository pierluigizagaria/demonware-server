package lobby

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

func psnTicket(issued time.Time) []byte {
	ticket := make([]byte, 128)
	binary.LittleEndian.PutUint32(ticket, auth.TicketMagic)
	ticket[4] = 1
	binary.LittleEndian.PutUint32(ticket[5:], 5760)
	binary.LittleEndian.PutUint32(ticket[13:], uint32(issued.Unix()))
	binary.LittleEndian.PutUint64(ticket[17:], 17)
	binary.LittleEndian.PutUint64(ticket[25:], 17)
	_, _ = rand.Read(ticket[33:97])
	copy(ticket[97:121], ticket[33:57])
	return ticket
}

func TestPS4LobbyAdmitsOnlyFreshIssuedTickets(t *testing.T) {
	tickets := auth.NewTicketStore()
	testPresence := presence.NewStore(nil)
	issueTestTicket := func(ticket []byte, id platform.PlayerID) {
		tickets.RememberTicket(ticket, id, time.Now(), ps4.TicketWindow)
	}
	if l := handshake(t, tickets, testPresence, psnTicket(time.Now())); l.Keys != nil {
		t.Fatal("unissued self-supplied ticket established encrypted session")
	}
	fresh := psnTicket(time.Now())
	issueTestTicket(fresh, platform.PlayerID{PID: 17, Username: "fixture", Sub: "shadnet:17", Kind: "shadnet", Title: 5760})
	l := handshake(t, tickets, testPresence, fresh)
	defer testPresence.DropOnline(l.N)
	if l.Keys == nil || l.Player == nil || l.Player.PID != 17 || l.Player.Title != 5760 {
		t.Fatal("fresh issued ticket refused or identity lost")
	}
	stale := psnTicket(time.Now().Add(-121 * time.Second))
	issueTestTicket(stale, platform.PlayerID{PID: 17, Username: "fixture"})
	if l := handshake(t, tickets, testPresence, stale); l.Keys != nil {
		t.Fatal("expired publisher ticket admitted")
	}
	foreign := psnTicket(time.Now())
	issueTestTicket(foreign, platform.PlayerID{PID: 18, Username: "other"})
	if l := handshake(t, tickets, testPresence, foreign); l.Keys != nil {
		t.Fatal("ticket bound to another account admitted")
	}
}

// handshake sends a client 0x82 carrying ticket, tagged with the ticket's own
// lobby key, and returns the connection so callers can see if it was keyed.
func handshake(t *testing.T, tickets *auth.TicketStore, state *presence.Store, ticket []byte) *Session {
	t.Helper()
	l, _ := sharedPS4Lobby()
	l.Tickets, l.Presence = tickets, state
	l.Keys, l.Player = nil, nil
	l.MaxLen, l.Nonce, l.Payload1 = 65536, make([]byte, 8), make([]byte, 20)
	raw := demonware.Frame(append(append([]byte{demonware.MsgClientAuth}, ticket...), make([]byte, 8)...))
	var transcript bytes.Buffer
	transcript.Write(demonware.Uint32LE(210))
	transcript.Write(demonware.Uint32LE(220))
	transcript.Write(demonware.Uint32LE(l.MaxLen))
	transcript.Write(l.Nonce)
	transcript.Write(demonware.Uint32LE(uint32(len(l.Payload1) + 2)))
	transcript.WriteByte(demonware.FrameFlag)
	transcript.WriteByte(demonware.MsgServerHi)
	transcript.Write(l.Payload1)
	transcript.Write(raw[:len(raw)-8])
	k := demonware.DeriveKeys(transcript.Bytes(), ticket[97:121])
	copy(raw[len(raw)-8:], k.ClientTag[:])
	l.OnClientAuth(raw)
	return l
}
