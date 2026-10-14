package lobby

import (
	"bytes"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// The HTTP code is written into fields 1 AND 2: bdHTTPProxyResponse reads field 2, the REST path
// (bdRESTLSGResponseMessageDeserializer) reads field 1. The body stays field 3.
func TestCTRHTTPProxyReplyEncoding(t *testing.T) {
	testReplies := &ReplyCounter{}
	body := []byte(`{"enrollments":[]}`)
	reply := testReplies.HTTPProxyReply(1, 200, body)
	i := 16 // transaction + error + task; structured replies have no result counts.
	if len(reply) < i+6 || reply[i] != demonware.TagStruct {
		t.Fatalf("no struct buffer immediately after task header: %x", reply)
	}
	sb := reply[i+6:] // demonware.TagStruct + (demonware.TagUint32 + u32 length)
	want := append([]byte{0x08, 0xC8, 0x01, 0x10, 0xC8, 0x01, 0x1A, byte(len(body))}, body...)
	if !bytes.Equal(sb, want) {
		t.Fatalf("struct buffer\n got %x\nwant %x", sb, want)
	}
}
