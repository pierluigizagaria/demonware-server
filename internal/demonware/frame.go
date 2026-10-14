package demonware

import (
	"crypto/hmac"
	"encoding/binary"
	"fmt"
)

const (
	FrameFlag      = 0xAB
	LobbyVersion   = 220 // the highest accepted; the client offers 210 and 220
	HelloLen       = 28
	MsgServerHi    = 0x81
	MsgClientAuth  = 0x82
	MsgServerOK    = 0x83
	MsgError       = 0x84
	MsgEncrypted   = 0x85
	MsgMigrateInit = 0x87
	MsgMigrateAck  = 0x88
	MsgMigrateGo   = 0x89
)

// Uint32LE encodes an untyped little-endian integer.
func Uint32LE(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

// Frame wraps a body (including its message type) with a length and flag.
func Frame(body []byte) []byte {
	out := make([]byte, 0, 5+len(body))
	out = append(out, Uint32LE(uint32(1+len(body)))...)
	out = append(out, FrameFlag)
	return append(out, body...)
}

// FindTicket extracts a 128-byte magic-prefixed ticket at any bit offset,
// searching LSB order before MSB order. It does not authenticate the ticket.
func FindTicket(raw []byte) []byte {
	magic := []byte{0xDE, 0xAD, 0xBD, 0xEF}
	bit := func(i int, msb bool) byte {
		b := raw[i/8]
		if msb {
			return (b >> (7 - uint(i%8))) & 1
		}
		return (b >> uint(i%8)) & 1
	}
	total := len(raw) * 8
	for _, msb := range []bool{false, true} {
		for off := 0; off+128*8 <= total; off++ {
			get := func(k int) byte {
				var v byte
				for j := 0; j < 8; j++ {
					if msb {
						v |= bit(off+k*8+j, true) << uint(7-j)
					} else {
						v |= bit(off+k*8+j, false) << uint(j)
					}
				}
				return v
			}
			ok := true
			for k := 0; k < 4 && ok; k++ {
				ok = get(k) == magic[k]
			}
			if !ok {
				continue
			}
			t := make([]byte, 128)
			for k := range t {
				t[k] = get(k)
			}
			return t
		}
	}
	return nil
}

// ReadFrame returns a raw frame including its length, or nil for a keepalive.
// readFull must return exactly n bytes or an error. The application supplies it
// so deadlines and connection lifetime remain outside the protocol package.
func ReadFrame(readFull func(n int) ([]byte, error), maxLen uint32) ([]byte, error) {
	hdr, err := readFull(4)
	if err != nil {
		return nil, err
	}
	if len(hdr) != 4 {
		return nil, ErrShort
	}
	n := binary.LittleEndian.Uint32(hdr)
	if n == 0 {
		return nil, nil
	}
	if n > maxLen || n > 1<<21 {
		return nil, fmt.Errorf("frame of %d bytes rejected", n)
	}
	rest, err := readFull(int(n))
	if err != nil {
		return nil, err
	}
	if len(rest) != int(n) {
		return nil, ErrShort
	}
	return append(hdr, rest...), nil
}

// Hello is the client's unframed version negotiation.
type Hello struct {
	MaxFrame uint32
	Nonce    [8]byte
}

func ParseHello(raw []byte) (Hello, error) {
	var h Hello
	if len(raw) != HelloLen {
		return h, ErrShort
	}
	for i, want := range []uint32{200, 200, 210, 220} {
		if binary.LittleEndian.Uint32(raw[i*4:]) != want {
			return h, fmt.Errorf("unsupported lobby versions")
		}
	}
	h.MaxFrame = binary.LittleEndian.Uint32(raw[16:])
	copy(h.Nonce[:], raw[20:])
	return h, nil
}

// ServerHelloPayload builds the 0x81 payload; ID allocation and entropy belong
// to the application. The client skips the eight padding bytes.
func ServerHelloPayload(id uint64, padding [8]byte) []byte {
	b := Uint32LE(LobbyVersion)
	b = binary.LittleEndian.AppendUint64(b, id)
	return append(b, padding[:]...)
}

// HandshakeTranscript excludes the final eight-byte client proof. authPrefix
// contains the entire 0x82 frame except that proof, including length and flag.
func HandshakeTranscript(maxFrame uint32, nonce, serverPayload, authPrefix []byte) []byte {
	b := Uint32LE(210)
	b = append(b, Uint32LE(220)...)
	b = append(b, Uint32LE(maxFrame)...)
	b = append(b, nonce...)
	b = append(b, Uint32LE(uint32(len(serverPayload)+2))...)
	b = append(b, FrameFlag, MsgServerHi)
	b = append(b, serverPayload...)
	return append(b, authPrefix...)
}

// EncryptedFrame borrows a raw frame. Sequence admission belongs to the caller.
// Verify must succeed before Decrypt is called for an untrusted frame.
type EncryptedFrame struct {
	Type     byte
	Sequence uint32
	raw      []byte
}

func ParseEncryptedFrame(raw []byte) (EncryptedFrame, error) {
	if len(raw) < 5+1+4+16+16+8 {
		return EncryptedFrame{}, ErrShort
	}
	return EncryptedFrame{Type: raw[5], Sequence: binary.LittleEndian.Uint32(raw[6:10]), raw: raw}, nil
}

func (f EncryptedFrame) Verify(macKey []byte) bool {
	if len(f.raw) < 50 {
		return false
	}
	n := len(f.raw) - 8
	return hmac.Equal(HMACSHA1(macKey, f.raw[:n])[:8], f.raw[n:])
}

// Message is the decoded inner envelope. Payload aliases Plaintext.
type Message struct {
	Type               byte
	Payload, Plaintext []byte
}

func (f EncryptedFrame) Decrypt(aesKey []byte) (Message, error) {
	if len(f.raw) < 50 {
		return Message{}, ErrShort
	}
	pt, err := DecryptCBC(aesKey, f.raw[10:26], f.raw[26:len(f.raw)-8])
	if err != nil {
		return Message{}, err
	}
	if len(pt) < 5 {
		return Message{}, ErrShort
	}
	n := uint64(binary.LittleEndian.Uint32(pt))
	if n > uint64(len(pt)-5) {
		return Message{}, ErrShort
	}
	return Message{Type: pt[4], Payload: pt[5 : 5+int(n)], Plaintext: pt}, nil
}

// EncodeEncrypted constructs a 0x85 frame. The caller owns sequence numbers,
// direction-specific keys and a fresh 16-byte IV for each production message.
func EncodeEncrypted(sequence uint32, innerType byte, payload, iv, aesKey, macKey []byte) ([]byte, error) {
	pt := Uint32LE(uint32(len(payload)))
	pt = append(pt, innerType)
	pt = append(pt, payload...)
	for len(pt)%16 != 0 {
		pt = append(pt, 0)
	}
	ct, err := EncryptCBC(aesKey, iv, pt)
	if err != nil {
		return nil, err
	}
	body := append([]byte{MsgEncrypted}, Uint32LE(sequence)...)
	body = append(body, iv...)
	body = append(body, ct...)
	out := append(Uint32LE(uint32(1+len(body)+8)), FrameFlag)
	out = append(out, body...)
	return append(out, HMACSHA1(macKey, out)[:8]...), nil
}
