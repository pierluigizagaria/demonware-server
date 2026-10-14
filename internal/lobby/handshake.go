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

package lobby

import (
	hmac "crypto/hmac"
	rand "crypto/rand"
	hex "encoding/hex"
	fmt "fmt"
	authpkg "github.com/NextendoNetwork/nextendo-demonware/internal/auth"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	diagnosticspkg "github.com/NextendoNetwork/nextendo-demonware/internal/diagnostics"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
	io "io"
	net "net"
	os "os"
	filepath "path/filepath"
	sync "sync"
	atomic "sync/atomic"
	time "time"
)

type LobbyOptions struct {
	serverIDs        atomic.Uint64
	lobbyDocUpdateID atomic.Uint64
	MigrateEnabled   bool
	PublicHost       string
}

type LobbySession struct {
	Table     *ServiceTable
	Telemetry sessionTelemetry
	*diagnosticspkg.Diagnostics
	Options *LobbyOptions

	*ReplyCounter
	Presence *presencepkg.PresenceStore
	Tickets  *authpkg.TicketStore
	Clock    func() time.Time
	Entropy  io.Reader
	C        net.Conn
	N        uint64
	dumpDir  string

	MaxLen   uint32
	Nonce    []byte
	Payload1 []byte // the sent 0x81's payload, for the transcript

	Keys     *demonwarepkg.SessionKeys
	Player   *platformpkg.PlayerID
	S2cSeq   uint32
	C2sSeq   uint32 // last accepted client frame number
	sendMu   sync.Mutex
	msgSeen  int
	stateMu  sync.Mutex
	stopping bool

	// Sent after the in-flight task reply (pushed messages).
	AfterReplyActive bool
	ReplyErr         error // last attempted reply, retained for diagnostics
}

func (l *LobbySession) now() time.Time {
	if l.Clock != nil {
		return l.Clock()
	}
	return time.Now()
}

func (l *LobbySession) random(p []byte) (int, error) {
	if l.Entropy != nil {
		return io.ReadFull(l.Entropy, p)
	}
	return rand.Read(p)
}

func (l *LobbySession) logf(format string, a ...any) {
	l.DiagnosticLogf("[lobby] #%d "+format, append([]any{l.N}, a...)...)
}

func (l *LobbySession) readFull(n int) ([]byte, error) {
	b := make([]byte, n)
	l.stateMu.Lock()
	if l.stopping {
		l.stateMu.Unlock()
		return nil, net.ErrClosed
	}
	// Long: a connection kept silent must last as long as the game keeps it.
	if l.Keys == nil {
		_ = l.C.SetReadDeadline(time.Now().Add(15 * time.Second))
	} else {
		_ = l.C.SetReadDeadline(time.Time{})
	}
	l.stateMu.Unlock()
	_, err := io.ReadFull(l.C, b)
	return b, err
}

// Only operator shutdown sets stopping. Established sessions have no idle or
// elapsed-time deadline; an active handler retains its write opportunity.
func (l *LobbySession) RequestStop() {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	l.stopping = true
	_ = l.C.SetReadDeadline(time.Now())
}

func (l *LobbySession) isStopping() bool {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	return l.stopping
}

// readFrame returns the RAW frame (length included), nil for a keepalive.
func (l *LobbySession) readFrame() ([]byte, error) {
	return demonwarepkg.ReadFrame(l.readFull, l.MaxLen)
}

func (l *LobbySession) send(b []byte) error {
	_ = l.C.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := l.C.Write(b)
	return err
}

func (l *LobbySession) dump(tag string, b []byte) {
	if l.RedactLogs || l.dumpDir == "" {
		return
	}
	name := fmt.Sprintf("lobby_%03d_%s.bin", l.N, tag)
	_ = os.WriteFile(filepath.Join(l.dumpDir, name), b, 0o644)
}

func (l *LobbySession) run() {
	defer l.C.Close()
	defer l.Table.CloseSession(l)
	defer l.Presence.DropOnline(l.N)
	l.logf("==== CONNECT from=%s", l.C.RemoteAddr())

	hello, err := l.readFull(demonwarepkg.HelloLen)
	if err != nil {
		l.logf("hello: %v", err)
		return
	}
	l.dump("hello", hello)
	h, err := demonwarepkg.ParseHello(hello)
	if err != nil {
		l.logf("unexpected hello: %v", err)
		return
	}
	l.MaxLen = h.MaxFrame
	l.Nonce = h.Nonce[:]
	l.logf("hello ok: versions 210..220 maxFrame=0x%X nonce=%X", l.MaxLen, l.Nonce)

	// 0x81: version | server identifier (conn+0x268, surfaced to the game in
	// the "connected" event) | 8 bytes the client only skips over.
	id := 0x0D3000000000 + l.Options.serverIDs.Add(1)
	var pad [8]byte
	_, _ = rand.Read(pad[:])
	l.Payload1 = demonwarepkg.ServerHelloPayload(id, pad)
	if err := l.send(demonwarepkg.Frame(append([]byte{demonwarepkg.MsgServerHi}, l.Payload1...))); err != nil {
		l.logf("send 0x81: %v", err)
		return
	}
	l.logf("-> 0x81 version=%d id=0x%X", demonwarepkg.LobbyVersion, id)

	for {
		raw, err := l.readFrame()
		if l.isStopping() {
			return
		}
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
		case l.Keys == nil && body[0] == demonwarepkg.MsgClientAuth:
			if !l.OnClientAuth(raw) {
				return
			}
		case l.Keys == nil:
			l.logf("type 0x%02X before the handshake:\n%s", body[0], hex.Dump(raw))
		default:
			l.OnEncrypted(raw)
		}
	}
}

// onClientAuth checks the 0x82's tag against each candidate key, keeps the
// one that matches, and replies with 0x83.
func (l *LobbySession) OnClientAuth(raw []byte) bool {
	if len(raw) < 5+1+8 {
		l.logf("0x82 too short")
		return false
	}
	tag := raw[len(raw)-8:]

	transcript := demonwarepkg.HandshakeTranscript(l.MaxLen, l.Nonce, l.Payload1, raw[:len(raw)-8])

	candidates := l.Candidates(raw)
	for _, cand := range candidates {
		for _, wrapped := range []bool{false, true} {
			k24 := cand.Key24
			if wrapped {
				k24 = demonwarepkg.WrapKey24(k24, bdPublicKeyDER)
			}
			k := demonwarepkg.DeriveKeys(transcript, k24)
			if hmac.Equal(k.ClientTag[:], tag) {
				l.Keys = &k
				l.logf("0x82 AUTHENTICATED — key=%s rsa=%v", cand.name, wrapped)
				if tk := demonwarepkg.FindTicket(raw); tk != nil {
					l.LoadIdentity(tk)
				}
				if err := l.send(demonwarepkg.Frame(append([]byte{demonwarepkg.MsgServerOK}, k.ServerCheck[:]...))); err != nil {
					l.logf("send 0x83: %v", err)
					return false
				}
				l.logf("-> 0x83 chk=%X ; encrypted session established", k.ServerCheck)
				// The 0x83 already sets [+0x258]=3, the only condition for getStatus()==2: the 0x87 adds
				// nothing and makes the game wait for a migration (closes after ~12 s). Kept behind CTR_MIGRATE.
				if l.Options.MigrateEnabled {
					if err := l.sendEncrypted(demonwarepkg.MsgMigrateInit, []byte{1}); err != nil {
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
	Key24 []byte
}

// candidates gathers the plausible 24-byte keys. Only a fresh ticket issued
// here may key the handshake: the one the 0x82 echoes.
func (l *LobbySession) Candidates(raw []byte) []keyCand {
	var out []keyCand
	add := func(name string, t []byte) {
		out = append(out,
			keyCand{name + "[97:121]", append([]byte{}, t[97:121]...)},
			keyCand{name + "[33:57]", append([]byte{}, t[33:57]...)},
		)
	}
	if t := demonwarepkg.FindTicket(raw); t != nil {
		if _, ok := l.Tickets.IssuedTicketPlayer(t, time.Now(), ps4pkg.TicketWindow); ok {
			add("issued-ticket", t)
		}
	}
	return out
}

// onEncrypted applies the connection's admission and sequence rules before
// dispatching the decoded protocol message to the application.
func (l *LobbySession) OnEncrypted(raw []byte) {
	encrypted, err := demonwarepkg.ParseEncryptedFrame(raw)
	if err != nil {
		l.logf("encrypted frame rejected: %v", err)
		return
	}
	if !encrypted.Verify(l.Keys.C2SMAC) {
		l.logf("msg 0x%02X seq=%d invalid MAC, ignored", encrypted.Type, encrypted.Sequence)
		return
	}
	// Preserve the CTR-confirmed sequence policy, including consuming an
	// authenticated malformed payload's sequence before attempting to decode it.
	if encrypted.Sequence != l.C2sSeq+1 {
		l.logf("msg 0x%02X seq=%d expected %d, ignored", encrypted.Type, encrypted.Sequence, l.C2sSeq+1)
		return
	}
	l.C2sSeq = encrypted.Sequence
	message, err := encrypted.Decrypt(l.Keys.C2SAES)
	if err != nil {
		l.logf("msg 0x%02X seq=%d decrypt: %v", encrypted.Type, encrypted.Sequence, err)
		return
	}
	l.dump(fmt.Sprintf("in%02d_plain", l.msgSeen), message.Plaintext)
	inner, n := message.Type, len(message.Payload)
	if l.Verbose {
		l.logf("msg 0x%02X seq=%d inner_len=%d inner_type=0x%02X plaintext:\n%s", encrypted.Type, encrypted.Sequence, n, inner, hex.Dump(message.Plaintext))
	}
	if inner == demonwarepkg.InnerTaskRequest {
		l.ProcessTask(message.Payload)
		return
	}

	if inner == demonwarepkg.MsgMigrateAck {
		// 0x88 closes 0x87: nothing is expected after it. A 0x89 would trigger a real migration.
		if !l.Options.MigrateEnabled {
			l.logf("0x88 migrate-ack — LSG login complete")
			return
		}
		if err := l.sendEncrypted(demonwarepkg.MsgMigrateGo, l.Options.migrateGoPayload()); err != nil {
			l.logf("sending 0x89: %v", err)
			return
		}
		l.logf("-> 0x89 migrate-go %s:%d", l.Options.migrateIP(), migratePort)
		return
	}
	l.logf("internal message 0x%02X ignored (%d bytes)", inner, n)
}

// The game restarts the full handshake on the migrated connection, so it is
// sent back to the usual listening port.
const migratePort uint16 = 3074

func (options *LobbyOptions) migrateIP() net.IP {
	ip := net.ParseIP(options.PublicHost).To4()
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

func (options *LobbyOptions) migrateGoPayload() []byte {
	p := []byte{1, 2, migrateAddrLen}
	p = append(p, options.migrateIP()...)
	p = append(p, byte(migratePort&0xFF), byte(migratePort>>8))
	p = append(p, 0, 0)
	d := migrateDelayMS
	return append(p, byte(d), byte(d>>8), byte(d>>16), byte(d>>24))
}

// sendEncrypted emits a 0x85: u32 seq | iv | AES-CBC(u32 N | type | payload | padding) | tag.
func (l *LobbySession) sendEncrypted(innerType byte, payload []byte) error {
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return err
	}
	out, err := demonwarepkg.EncodeEncrypted(l.S2cSeq+1, innerType, payload, iv, l.Keys.S2CAES, l.Keys.S2CMAC)
	if err != nil {
		return err
	}
	l.S2cSeq++
	return l.send(out)
}
