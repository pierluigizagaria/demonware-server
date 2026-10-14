package main

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

// Legacy fixtures share this explicit test owner. Production has no fallback
// ticket directory; auth, lobby and Umbrella receive the same instance owner.
var testTickets = newTicketStore()

func testAuthHandler(p publisherPlatform, s *ctrAuthSigner, dumps string) http.Handler {
	return newPlatformAuthHandler(testTickets, testPresence, p, s, dumps)
}

func testUmbrellaHandler(w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
	handleCTRUmbrella(testTickets, w, r, body, n, dumps)
}

func TestInstanceTicketDirectoriesAreIsolated(t *testing.T) {
	a, b := newTicketStore(), newTicketStore()
	now := time.Now()
	ticket := psnTicket(now)
	a.rememberTicket(ticket, playerID{PID: 17, Username: "first", Title: 5760}, now, ticketWindow)
	if _, ok := b.issuedTicketPlayer(ticket, now, ticketWindow); ok {
		t.Fatal("another instance admitted a foreign ticket")
	}
	b.rememberTicket(ticket, playerID{PID: 17, Username: "second", Title: 5760}, now, ticketWindow)
	first, okA := a.issuedIdentity(ticket)
	second, okB := b.issuedIdentity(ticket)
	if !okA || !okB || first.Username != "first" || second.Username != "second" {
		t.Fatal("same numeric identity crossed instance directories")
	}
}

var testPresence = newPresenceStore(nil)

func init()                                   { ctrMM.onlinePIDs = testPresence.onlinePIDs }
func testBuildStats() apiStats                { return buildStats(testPresence) }
func testBuildGameStats(game string) apiStats { return buildGameStats(testPresence, game) }

func TestInstancePresenceAndPersistentNamesAreIsolated(t *testing.T) {
	firstDB := openTestCTR(t, filepath.Join(t.TempDir(), ctrDB), false)
	secondDB := openTestCTR(t, filepath.Join(t.TempDir(), ctrDB), false)
	a, b := newPresenceStore(firstDB), newPresenceStore(secondDB)
	now := time.Now()
	if a.rememberPlayerName(playerID{PID: 17, Username: "first"}, now) != nil || b.rememberPlayerName(playerID{PID: 17, Username: "second"}, now) != nil {
		t.Fatal("record names")
	}
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.presence, second.presence = a, b
	first.player.Username, second.player.Username = "first", "second"
	a.online[first.n], b.online[second.n] = first.player, second.player
	aw, bw := &architectureWire{}, &architectureWire{}
	first.c, second.c = aw, bw
	for _, tc := range []struct {
		l     *lobbyConn
		w     *architectureWire
		name  string
		value byte
	}{{first, aw, "first", 1}, {second, bw, "second", 2}} {
		q := demonware.NewWriter([]byte{12, 3, 10})
		q.Uint32(2)
		q.String("first")
		q.String("second")
		p := architectureReply(t, architectureCall(t, tc.l, tc.w, q.Bytes()), 10, 0)
		r := demonware.NewReader(p[26:])
		pid, _ := r.Uint64()
		name, err := r.String()
		if err != nil || pid != 17 || name != tc.name || len(r.Remaining()) != 0 {
			t.Fatal("name crossed instance boundary")
		}
		q = demonware.NewWriter([]byte{68, 3, 3})
		q.Raw(rpSetRequest("fixture", 17, "psn", 1, []byte{tc.value}))
		architectureReply(t, architectureCall(t, tc.l, tc.w, q.Bytes()), 3, 0)
	}
	if !bytes.Equal(a.rich[17].data, []byte{1}) || !bytes.Equal(b.rich[17].data, []byte{2}) {
		t.Fatal("presence crossed instance boundary")
	}
	a.dropOnline(first.n)
	if len(a.rich) != 0 || b.nicknameOnline(17) != "second" || len(b.rich) != 1 {
		t.Fatal("disconnect affected another instance")
	}
	q := demonware.NewWriter([]byte{12, 3, 10})
	q.Uint32(1)
	q.String("first")
	p, _ := hex.DecodeString(architectureCall(t, first, aw, q.Bytes())[0].Payload)
	r := demonware.NewReader(p[26:])
	pid, _ := r.Uint64()
	name, err := r.String()
	if err != nil || pid != 17 || name != "first" {
		t.Fatal("persistent name lost on disconnect")
	}
}
