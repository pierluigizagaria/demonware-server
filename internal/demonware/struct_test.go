package demonware_test

import (
	"bytes"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

func TestStructFieldsAndLimits(t *testing.T) {
	b := demonware.AppendVarintField(nil, 1, demonware.ZigZag(-123))
	b = demonware.AppendBytesField(b, 32, []byte("fixture"))
	fields, err := demonware.ParseFields(b)
	if err != nil || len(fields) != 2 || fields[0].Number != 1 || fields[0].Value != 245 || fields[1].Number != 32 || string(fields[1].Data) != "fixture" {
		t.Fatal("struct fields decoded incorrectly")
	}
	for _, bad := range [][]byte{
		{0}, {8, 128}, {8, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 1},
		{10, 255, 255, 255, 255, 255, 255, 255, 255, 255, 1}, {10, 2, 1}, {13, 1, 2, 3, 4},
		bytes.Repeat([]byte{8, 0}, 4097), make([]byte, (1<<20)+1),
	} {
		if _, err := demonware.ParseFields(bad); err == nil {
			t.Fatal("malformed or oversized struct accepted")
		}
	}
	for _, off := range []int{-1, 0, 1} {
		if _, _, err := demonware.ReadVarint(nil, off); err == nil {
			t.Fatal("invalid varint offset accepted")
		}
	}
	w := demonware.NewWriter([]byte{80, 3, 245})
	w.Struct(b)
	if !bytes.Equal(demonware.TaskStructPayload(w.Bytes()), b) {
		t.Fatal("task struct wrapper changed")
	}
	for n := 0; n < len(w.Bytes()); n++ {
		if demonware.TaskStructPayload(w.Bytes()[:n]) != nil {
			t.Fatal("truncated struct wrapper accepted")
		}
	}
}

func TestTicketBitOffsets(t *testing.T) {
	ticket := make([]byte, 128)
	copy(ticket, []byte{0xde, 0xad, 0xbd, 0xef})
	for i := 4; i < len(ticket); i++ {
		ticket[i] = byte(i)
	}
	for _, msb := range []bool{false, true} {
		for offset := 0; offset < 8; offset++ {
			raw := make([]byte, 129)
			for i := 0; i < 128*8; i++ {
				source, target := uint(i%8), uint((offset+i)%8)
				if msb {
					source = 7 - source
					target = 7 - target
				}
				raw[(offset+i)/8] |= ((ticket[i/8] >> source) & 1) << target
			}
			if !bytes.Equal(demonware.FindTicket(raw), ticket) {
				t.Fatalf("ticket lost: msb=%t offset=%d", msb, offset)
			}
		}
	}
	if demonware.FindTicket(ticket[:127]) != nil {
		t.Fatal("short ticket accepted")
	}
}

// Seeds run in ordinary go test; bounded fuzzing exercises untrusted parsing.
func FuzzWireParsers(f *testing.F) {
	for _, seed := range [][]byte{nil, {3, 1}, {16, 'x', 0}, {19, 8, 255, 255, 255, 255}, {8, 150, 1}, {10, 1, 0}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			return
		}
		_, _ = demonware.ParseFields(b)
		_ = demonware.TaskStructPayload(b)
		for _, read := range []func(*demonware.Reader){
			func(r *demonware.Reader) { r.Uint8() }, func(r *demonware.Reader) { r.Uint32() },
			func(r *demonware.Reader) { r.Uint64() }, func(r *demonware.Reader) { r.Int64() },
			func(r *demonware.Reader) { r.Bool() }, func(r *demonware.Reader) { r.String() },
			func(r *demonware.Reader) { r.Blob() }, func(r *demonware.Reader) { r.Uint64s() },
		} {
			r := demonware.NewReader(b)
			read(r)
			if r.Position() > len(b) {
				t.Fatal("cursor outside input")
			}
			_ = r.Remaining()
		}
		if frame, err := demonware.ParseEncryptedFrame(b); err == nil {
			frame.Verify(make([]byte, 20))
			frame.Decrypt(make([]byte, 16))
		}
	})
}
