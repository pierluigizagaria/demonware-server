package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func testCTRSigner(t *testing.T) *auth.CTRSigner {
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
	s, err := auth.LoadCTRSigner(path)
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
	l, _ := sharedPS4Lobby()
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

func TestSharedGetUserIDsResolvesOnlineAndKnownIdentities(t *testing.T) {
	l, w := sharedPS4Lobby()
	withTestPlayerNames(t)
	testPresence.OnlineMu.Lock()
	testPresence.Online[l.N] = l.Player
	testPresence.OnlineMu.Unlock()
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
