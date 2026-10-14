package main

// Demonware lobby handshake. The layout and offsets below are inherited from
// upstream's D3 NSO reverse-engineering notes (original reference:
// d3hack/capture/nso/handshake*_decomp.txt), not new local binary evidence.
// Wire helpers live in internal/demonware; admission and connection state stay here.
//
// Connect (0xBFC8F0): the client draws 8 random bytes (conn+0x21C) and sends
// 28 RAW bytes, unframed:
//
//	u32 200 | u32 200 | u32 210 | u32 220 | u32 maxFrame | nonce[8]
//
// After that everything is framed (reader 0xBFA950):
//
//	u32 L | u8 flag | body[L-1]        L=0: keepalive
//
// with the body's first byte being the type. States of conn+0x210 (0xBFAB90):
//
//	1  waits for 0x81  (0xBFC290)  u32 version 210..220 | u64 (conn+0x268) | 8 bytes
//	   -> the client replies with 0x82 (0xBFBB80) and derives the keys
//	2  waits for 0x83  (0xBFBAE0)  u64 == conn+0x224 (CLIENTCHAL[8:16])  | 0x84 = error u32
//	3  waits for 0x85  (0xBFB730)  u32 seq | iv[16] | AES-CBC | tag[8]    | 0x84 = error u32
//
// Transcript signed by both sides (0xBFBB80):
//
//	u32 210 | u32 220 | u32 maxFrame | nonce[8]
//	| u32 (len81+2) | 0xAB | 0x81 | payload81
//	| the 0x82 frame in full minus its last 8 bytes

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

var serverIDs atomic.Uint64

type lobbyConn struct {
	clock   func() time.Time
	entropy io.Reader
	c       net.Conn
	n       uint64
	dumpDir string
	pubDir  string

	maxLen   uint32
	nonce    []byte
	payload1 []byte // the sent 0x81's payload, for the transcript

	keys    *demonware.SessionKeys
	player  *playerID
	s2cSeq  uint32
	c2sSeq  uint32 // last accepted client frame number
	sendMu  sync.Mutex
	msgSeen int

	// Last host document received in 145/2, returned as-is in 145/3.
	lobbyDoc string

	// Sent after the in-flight task reply (pushed messages).
	afterReply func()
	replyErr   error // outcome of the last task reply, for afterReply
}

func (l *lobbyConn) now() time.Time {
	if l.clock != nil {
		return l.clock()
	}
	return time.Now()
}

func (l *lobbyConn) random(p []byte) (int, error) {
	if l.entropy != nil {
		return io.ReadFull(l.entropy, p)
	}
	return rand.Read(p)
}

func (l *lobbyConn) logf(format string, a ...any) {
	diagnosticLogf("#%d "+format, append([]any{l.n}, a...)...)
}

func (l *lobbyConn) readFull(n int) ([]byte, error) {
	b := make([]byte, n)
	// Long: a connection kept silent must last as long as the game keeps it.
	if l.keys == nil {
		_ = l.c.SetReadDeadline(time.Now().Add(15 * time.Second))
	} else {
		_ = l.c.SetReadDeadline(time.Time{})
	}
	_, err := io.ReadFull(l.c, b)
	return b, err
}

// readFrame returns the RAW frame (length included), nil for a keepalive.
func (l *lobbyConn) readFrame() ([]byte, error) {
	return demonware.ReadFrame(l.readFull, l.maxLen)
}

func (l *lobbyConn) send(b []byte) error {
	_ = l.c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := l.c.Write(b)
	return err
}

func (l *lobbyConn) dump(tag string, b []byte) {
	if redactLogs || l.dumpDir == "" {
		return
	}
	name := fmt.Sprintf("lobby_%03d_%s.bin", l.n, tag)
	_ = os.WriteFile(filepath.Join(l.dumpDir, name), b, 0o644)
}

func (l *lobbyConn) run() {
	defer l.c.Close()
	defer dropSessionsOf(l.n)
	defer func() { sendCTRNotices(ctrMM.leave(l, 0)) }()
	defer dropOnline(l.n)
	l.logf("==== CONNECT from=%s", l.c.RemoteAddr())

	hello, err := l.readFull(demonware.HelloLen)
	if err != nil {
		l.logf("hello: %v", err)
		return
	}
	l.dump("hello", hello)
	h, err := demonware.ParseHello(hello)
	if err != nil {
		l.logf("unexpected hello: %v", err)
		return
	}
	l.maxLen = h.MaxFrame
	l.nonce = h.Nonce[:]
	l.logf("hello ok: versions 210..220 maxFrame=0x%X nonce=%X", l.maxLen, l.nonce)

	// 0x81: version | server identifier (conn+0x268, surfaced to the game in
	// the "connected" event) | 8 bytes the client only skips over.
	id := 0x0D3000000000 + serverIDs.Add(1)
	var pad [8]byte
	_, _ = rand.Read(pad[:])
	l.payload1 = demonware.ServerHelloPayload(id, pad)
	if err := l.send(demonware.Frame(append([]byte{demonware.MsgServerHi}, l.payload1...))); err != nil {
		l.logf("send 0x81: %v", err)
		return
	}
	l.logf("-> 0x81 version=%d id=0x%X", demonware.LobbyVersion, id)

	for {
		raw, err := l.readFrame()
		if err != nil {
			if err != io.EOF {
				l.logf("end: %v", err)
			} else {
				l.logf("end: client closed")
			}
			return
		}
		if raw == nil {
			// Keepalive: we send the same back, which is what the reader accepts.
			_ = l.send(make([]byte, 4))
			continue
		}
		body := raw[5:]
		if len(body) == 0 {
			continue
		}
		l.msgSeen++
		l.dump(fmt.Sprintf("in%02d_%02x", l.msgSeen, body[0]), raw)

		switch {
		case l.keys == nil && body[0] == demonware.MsgClientAuth:
			if !l.onClientAuth(raw) {
				return
			}
		case l.keys == nil:
			l.logf("type 0x%02X before the handshake:\n%s", body[0], hex.Dump(raw))
		default:
			l.onEncrypted(raw)
		}
	}
}

// onClientAuth checks the 0x82's tag against each candidate key, keeps the
// one that matches, and replies with 0x83.
func (l *lobbyConn) onClientAuth(raw []byte) bool {
	if len(raw) < 5+1+8 {
		l.logf("0x82 too short")
		return false
	}
	tag := raw[len(raw)-8:]

	transcript := demonware.HandshakeTranscript(l.maxLen, l.nonce, l.payload1, raw[:len(raw)-8])

	candidates := l.candidates(raw)
	for _, cand := range candidates {
		for _, wrapped := range []bool{false, true} {
			k24 := cand.key24
			if wrapped {
				k24 = demonware.WrapKey24(k24, bdPublicKeyDER)
			}
			k := demonware.DeriveKeys(transcript, k24)
			if hmac.Equal(k.ClientTag[:], tag) {
				l.keys = &k
				l.logf("0x82 AUTHENTICATED — key=%s rsa=%v", cand.name, wrapped)
				if tk := demonware.FindTicket(raw); tk != nil {
					l.loadIdentity(tk)
				}
				if err := l.send(demonware.Frame(append([]byte{demonware.MsgServerOK}, k.ServerCheck[:]...))); err != nil {
					l.logf("send 0x83: %v", err)
					return false
				}
				l.logf("-> 0x83 chk=%X ; encrypted session established", k.ServerCheck)
				// The 0x83 already sets [+0x258]=3, the only condition for getStatus()==2: the 0x87 adds
				// nothing and makes the game wait for a migration (closes after ~12 s). Kept behind CTR_MIGRATE.
				if migrateEnabled {
					if err := l.sendEncrypted(demonware.MsgMigrateInit, []byte{1}); err != nil {
						l.logf("send 0x87: %v", err)
						return false
					}
					l.logf("-> 0x87 migrate-init")
				}
				return true
			}
		}
	}

	// Failure: we do NOT close. A silent lobby connection leaves the game
	// "friends only" online (P2P invites); a close or an invalid frame flips
	// it offline after a few tries.
	l.logf("0x82: no key reproduces tag %X (%d candidates) — connection kept silent\n%s", tag, len(candidates), hex.Dump(raw))
	return true
}

type keyCand struct {
	name  string
	key24 []byte
}

// candidates gathers the plausible 24-byte keys. Only a fresh ticket issued
// here may key the handshake: the one the 0x82 echoes.
func (l *lobbyConn) candidates(raw []byte) []keyCand {
	var out []keyCand
	add := func(name string, t []byte) {
		out = append(out,
			keyCand{name + "[97:121]", append([]byte{}, t[97:121]...)},
			keyCand{name + "[33:57]", append([]byte{}, t[33:57]...)},
		)
	}
	if t := demonware.FindTicket(raw); t != nil {
		if _, ok := issuedTicketPlayer(t, time.Now(), ticketWindow); ok {
			add("issued-ticket", t)
		}
	}
	return out
}

// onEncrypted applies the connection's admission and sequence rules before
// dispatching the decoded protocol message to the application.
func (l *lobbyConn) onEncrypted(raw []byte) {
	encrypted, err := demonware.ParseEncryptedFrame(raw)
	if err != nil {
		l.logf("encrypted frame rejected: %v", err)
		return
	}
	if !encrypted.Verify(l.keys.C2SMAC) {
		l.logf("msg 0x%02X seq=%d invalid MAC, ignored", encrypted.Type, encrypted.Sequence)
		return
	}
	// Preserve the CTR-confirmed sequence policy, including consuming an
	// authenticated malformed payload's sequence before attempting to decode it.
	if encrypted.Sequence != l.c2sSeq+1 {
		l.logf("msg 0x%02X seq=%d expected %d, ignored", encrypted.Type, encrypted.Sequence, l.c2sSeq+1)
		return
	}
	l.c2sSeq = encrypted.Sequence
	message, err := encrypted.Decrypt(l.keys.C2SAES)
	if err != nil {
		l.logf("msg 0x%02X seq=%d decrypt: %v", encrypted.Type, encrypted.Sequence, err)
		return
	}
	l.dump(fmt.Sprintf("in%02d_plain", l.msgSeen), message.Plaintext)
	inner, n := message.Type, len(message.Payload)
	if verbose {
		l.logf("msg 0x%02X seq=%d inner_len=%d inner_type=0x%02X plaintext:\n%s", encrypted.Type, encrypted.Sequence, n, inner, hex.Dump(message.Plaintext))
	}
	if inner == demonware.InnerTaskRequest {
		l.onTask(message.Payload)
		return
	}

	if inner == demonware.MsgMigrateAck {
		// 0x88 closes 0x87: nothing is expected after it. A 0x89 would trigger a real migration.
		if !migrateEnabled {
			l.logf("0x88 migrate-ack — LSG login complete")
			return
		}
		if err := l.sendEncrypted(demonware.MsgMigrateGo, migrateGoPayload()); err != nil {
			l.logf("sending 0x89: %v", err)
			return
		}
		l.logf("-> 0x89 migrate-go %s:%d", migrateIP(), migratePort)
		return
	}
	l.logf("internal message 0x%02X ignored (%d bytes)", inner, n)
}

// The game restarts the full handshake on the migrated connection, so it is
// sent back to the usual listening port.
const migratePort uint16 = 3074

var migrateEnabled bool // CTR_MIGRATE (config.go)

func migrateIP() net.IP {
	ip := net.ParseIP(nextendoHost).To4()
	if ip == nil {
		ip = net.IPv4(127, 0, 0, 1).To4()
	}
	return ip
}

// migrateGoPayload: 01 | type 2 (address supplied) | address length | bdAddr | u16 token length |
// token | u32. parse220MigrateAck requires that trailing u32 (milliseconds, *0.001 and capped at
// 30s in +0x764): without it it returns false and process220MigrateAck closes the connection.
const migrateAddrLen = 6
const migrateDelayMS uint32 = 1000

func migrateGoPayload() []byte {
	p := []byte{1, 2, migrateAddrLen}
	p = append(p, migrateIP()...)
	p = append(p, byte(migratePort&0xFF), byte(migratePort>>8))
	p = append(p, 0, 0)
	d := migrateDelayMS
	return append(p, byte(d), byte(d>>8), byte(d>>16), byte(d>>24))
}

// sendEncrypted emits a 0x85: u32 seq | iv | AES-CBC(u32 N | type | payload | padding) | tag.
func (l *lobbyConn) sendEncrypted(innerType byte, payload []byte) error {
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return err
	}
	out, err := demonware.EncodeEncrypted(l.s2cSeq+1, innerType, payload, iv, l.keys.S2CAES, l.keys.S2CMAC)
	if err != nil {
		return err
	}
	l.s2cSeq++
	return l.send(out)
}
