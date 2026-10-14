package lobby

import (
	"encoding/binary"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// clientFrame encrypts one client 0x85 frame carrying a task request.
func clientFrame(t *testing.T, l *Session, seq uint32, task []byte) []byte {
	t.Helper()
	pt := binary.LittleEndian.AppendUint32(nil, uint32(len(task)))
	pt = append(pt, 0x86)
	pt = append(pt, task...)
	for len(pt)%16 != 0 {
		pt = append(pt, 0)
	}
	iv := make([]byte, 16)
	ct, err := demonware.EncryptCBC(l.Keys.C2SAES, iv, pt)
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte{demonware.MsgEncrypted}, binary.LittleEndian.AppendUint32(nil, seq)...)
	body = append(append(body, iv...), ct...)
	out := binary.LittleEndian.AppendUint32(nil, uint32(1+len(body)+8))
	out = append(append(out, demonware.FrameFlag), body...)
	return append(out, demonware.HMACSHA1(l.Keys.C2SMAC, out)[:8]...)
}

func TestEncryptedFramesFollowClientSequence(t *testing.T) {
	l, w := sharedPS4Lobby()
	task := (demonware.NewWriter([]byte{6, demonware.TagUint8, 1})).Bytes() // any request; the reply only needs to exist
	replied := func(raw []byte) bool {
		w.Reset()
		l.OnEncrypted(raw)
		return w.Len() > 0
	}
	first := clientFrame(t, l, 1, task)
	if !replied(first) {
		t.Fatal("first frame not processed")
	}
	if replied(first) {
		t.Fatal("replayed frame processed")
	}
	if replied(clientFrame(t, l, 3, task)) {
		t.Fatal("frame ahead of the sequence processed")
	}
	if !replied(clientFrame(t, l, 2, task)) {
		t.Fatal("next genuine frame refused after a dropped one")
	}
}

func TestEncryptedFrameRejectionPreservesSequencePolicy(t *testing.T) {
	l, w := sharedPS4Lobby()
	task := []byte{6, 3, 1}
	first := clientFrame(t, l, 1, task)
	tampered := append([]byte(nil), first...)
	tampered[len(tampered)-1] ^= 1
	for _, raw := range [][]byte{first[:20], tampered} {
		l.OnEncrypted(raw)
		if w.Len() != 0 || l.C2SSeq != 0 {
			t.Fatal("unauthenticated or truncated frame consumed a sequence or replied")
		}
	}
	l.OnEncrypted(first)
	if w.Len() == 0 || l.C2SSeq != 1 {
		t.Fatal("valid frame refused after rejection")
	}
	w.Reset()

	// Preserve the established rule: a correctly authenticated frame consumes
	// its sequence even when its inner payload length cannot be decoded.
	malformed := clientFrame(t, l, 2, task)
	pt := make([]byte, 16)
	binary.LittleEndian.PutUint32(pt, ^uint32(0))
	ct, err := demonware.EncryptCBC(l.Keys.C2SAES, malformed[10:26], pt)
	if err != nil {
		t.Fatal(err)
	}
	copy(malformed[26:], ct)
	n := len(malformed) - 8
	copy(malformed[n:], demonware.HMACSHA1(l.Keys.C2SMAC, malformed[:n])[:8])
	l.OnEncrypted(malformed)
	if w.Len() != 0 || l.C2SSeq != 2 {
		t.Fatal("authenticated malformed frame changed sequence policy")
	}
	l.OnEncrypted(clientFrame(t, l, 3, task))
	if w.Len() == 0 || l.C2SSeq != 3 {
		t.Fatal("next valid frame refused after malformed payload")
	}
}
