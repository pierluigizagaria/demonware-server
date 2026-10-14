package main

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// structBodyOf pulls the struct buffer back out of a task reply.
func structBodyOf(t *testing.T, reply []byte) []byte {
	t.Helper()
	const off = 1 + 8 + 1 + 4 // u64 transaction, u32 error
	if reply[off] != demonware.TagUint8 || reply[off+2] != demonware.TagStruct {
		t.Fatalf("not a struct reply: % x", reply[:off+3])
	}
	return reply[off+3+5:]
}

// A real purchaseSkus (task 154) request captured from production on 2026-09-21. The sku
// is the SECOND u32: the first is the count.
const realPurchase = "50039a106f6374616e655f73776974636800080100000008950f030008010000000801000000010008010000000a00000000000000001000080000000008000000000300080000000000"
