package lobby

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// bdStructBufferTask::deserializeTaskReply reads the struct straight after the task byte:
// a result count in between makes the task fail with error 4.
func TestStructTaskReplyLayout(t *testing.T) {
	testReplies := &ReplyCounter{}
	reply := testReplies.StructTaskReply(byte(245), []byte{0x0A, 0x02, 0x08, 0x01})

	if reply[0] != demonware.TagUint64 {
		t.Fatalf("reply[0] = %#x, want transaction tag %#x", reply[0], demonware.TagUint64)
	}
	// u64 tag + 8 bytes, u32 tag + 4 bytes, u8 tag + 1 byte.
	const taskOff = 1 + 8 + 1 + 4
	if reply[taskOff] != demonware.TagUint8 || reply[taskOff+1] != byte(245) {
		t.Fatalf("task byte not at %d: % x", taskOff, reply[:taskOff+2])
	}
	if reply[taskOff+2] != demonware.TagStruct {
		t.Fatalf("struct must follow the task byte, got %#x", reply[taskOff+2])
	}
}
