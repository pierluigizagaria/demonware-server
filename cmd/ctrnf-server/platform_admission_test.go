package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
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

// handshake sends a client 0x82 carrying ticket, tagged with the ticket's own
// lobby key, and returns the connection so callers can see if it was keyed.
func handshake(t *testing.T, ticket []byte) *lobbyConn {
	t.Helper()
	wire := &platformWire{}
	server := lobby.NewServer(testConfig, nil, testDiagnostics, testTickets, testPresence, testReplies, &atomic.Uint64{}, testTelemetry, testOptions)
	l := &lobbyConn{Session: server.NewConn(wire, ""), C: wire}
	raw, keys := fixtureClientAuth(ticket)
	l.OnClientAuth(raw)
	if l.ChannelSnapshot().Established {
		l.Keys = &keys
	}

	return l
}

func TestSharedGetUserIDsResolvesOnlineAndKnownIdentities(t *testing.T) {
	withTestPlayerNames(t)
	l, w := sharedPS4Lobby()

	testPresence.RegisterOnline(l.N, l.Player)

	defer testPresence.DropOnline(l.N)
	if err := testPresence.RememberPlayerName(platform.PlayerID{PID: 42, Username: "OfflineFriend", Kind: "shadnet"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	req := demonware.NewWriter([]byte{12, demonware.TagUint8, 10})
	req.Uint32(3)
	req.String("FIXTURE")
	req.String("offlinefriend")
	req.String("stranger")
	req.Raw([]byte{0})
	reply := sharedTask(t, l, w, req.Bytes())
	if len(reply) < 21 || binary.LittleEndian.Uint32(reply[10:14]) != 0 || binary.LittleEndian.Uint32(reply[17:21]) != 2 {
		t.Fatal("online and previously authenticated friends were not both resolved")
	}
	if !bytes.Contains(reply, []byte("OfflineFriend\x00")) || bytes.Contains(reply, []byte("stranger")) {
		t.Fatal("wrong identities resolved")
	}
}
