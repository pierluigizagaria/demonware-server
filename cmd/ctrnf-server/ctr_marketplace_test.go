package main

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// The exact bytes Crash Team Racing sent for bdMarketplace::getBalancesV3 (service 80,
// task 245), captured from production on 2026-09-21.
const realBalancesRequest = "5003f51708110000000a0d6f6374616e655f7377697463681003"

// bdStructBufferTask::deserializeTaskReply reads the struct straight after the task byte:
// a result count in between makes the task fail with error 4.
func TestStructTaskReplyLayout(t *testing.T) {
	reply := testReplies.StructTaskReply(ctr.TaskGetBalancesV3, []byte{0x0A, 0x02, 0x08, 0x01})

	if reply[0] != demonware.TagUint64 {
		t.Fatalf("reply[0] = %#x, want transaction tag %#x", reply[0], demonware.TagUint64)
	}
	// u64 tag + 8 bytes, u32 tag + 4 bytes, u8 tag + 1 byte.
	const taskOff = 1 + 8 + 1 + 4
	if reply[taskOff] != demonware.TagUint8 || reply[taskOff+1] != ctr.TaskGetBalancesV3 {
		t.Fatalf("task byte not at %d: % x", taskOff, reply[:taskOff+2])
	}
	if reply[taskOff+2] != demonware.TagStruct {
		t.Fatalf("struct must follow the task byte, got %#x", reply[taskOff+2])
	}
}

// structBodyOf pulls the struct buffer back out of a task reply.
func structBodyOf(t *testing.T, reply []byte) []byte {
	t.Helper()
	const off = 1 + 8 + 1 + 4 // u64 transaction, u32 error
	if reply[off] != demonware.TagUint8 || reply[off+2] != demonware.TagStruct {
		t.Fatalf("not a struct reply: % x", reply[:off+3])
	}
	return reply[off+3+5:]
}

func hasField(sb []byte, want byte) bool {
	for i := 0; i < len(sb); {
		field, wire := sb[i]>>3, sb[i]&7
		i++
		if wire != 2 {
			return false
		}
		ln, next, err := demonware.ReadVarint(sb, i)
		if err != nil {
			return false
		}
		if field == want {
			return true
		}
		i = next + int(ln)
	}
	return false
}

// A real purchaseSkus (task 154) request captured from production on 2026-09-21. The sku
// is the SECOND u32: the first is the count.
const realPurchase = "50039a106f6374616e655f73776974636800080100000008950f030008010000000801000000010008010000000a00000000000000001000080000000008000000000300080000000000"
