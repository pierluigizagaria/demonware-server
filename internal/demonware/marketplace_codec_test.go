package demonware

import (
	"bytes"
	"testing"
)

func TestStructPayloadRejectsJunk(t *testing.T) {
	if got := TaskStructPayload([]byte{0x50, 0x03, 0xf5}); got != nil {
		t.Fatalf("short buffer returned % x", got)
	}
	if got := TaskStructPayload(bytes.Repeat([]byte{0xff}, 32)); got != nil {
		t.Fatalf("junk returned % x", got)
	}
}

// The game read a raw 10000 as 5000, so the amount must survive a ZigZag round trip.
func TestAmountSurvivesZigzagRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, 7, 1234, 5000, 10000} {
		enc := ZigZag(v)
		dec := int64(enc>>1) ^ -int64(enc&1)
		if dec != v {
			t.Fatalf("ZigZag(%d) decoded to %d", v, dec)
		}
	}
	if ZigZag(10000) != 20000 {
		t.Fatalf("ZigZag(10000) = %d, want 20000 so the game shows 10000", ZigZag(10000))
	}
}
