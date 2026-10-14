package demonware

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Field is one field of the supported bdStructBuffer subset (varint or bytes).
// Data borrows the input buffer. Unknown field numbers are left to the caller.
type Field struct {
	Number, Wire, Value uint64
	Data                []byte
}

// AppendVarint appends an unsigned protobuf varint.
func AppendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadVarint returns the decoded value and the next offset; on error the offset
// is unchanged. Negative, overflowing and truncated inputs are rejected.
func ReadVarint(b []byte, i int) (uint64, int, error) {
	if i < 0 || i > len(b) {
		return 0, i, errors.New("bad varint offset")
	}
	v, n := binary.Uvarint(b[i:])
	if n <= 0 {
		return 0, i, errors.New("bad varint")
	}
	return v, i + n, nil
}

// AppendVarintField appends a wire-type-0 field. field must be nonzero.
func AppendVarintField(b []byte, field byte, v uint64) []byte {
	b = AppendVarint(b, uint64(field)<<3)
	return AppendVarint(b, v)
}

// AppendBytesField appends a wire-type-2 field. field must be nonzero.
func AppendBytesField(b []byte, field byte, p []byte) []byte {
	b = AppendVarint(b, uint64(field)<<3|2)
	b = AppendVarint(b, uint64(len(p)))
	return append(b, p...)
}

// ZigZag encodes a signed structured field; typed Int64 uses fixed-width bytes.
func ZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// TaskStructPayload borrows a structured remote task's arguments. A malformed
// or truncated wrapper returns nil. The three-byte task header is not decoded.
func TaskStructPayload(payload []byte) []byte {
	const hdr = 3 // service + TagUint8 + task
	if len(payload) < hdr+6 || payload[hdr] != TagStruct || payload[hdr+1] != TagUint32 {
		return nil
	}
	n := uint64(binary.LittleEndian.Uint32(payload[hdr+2:]))
	if n > uint64(len(payload)-hdr-6) {
		return nil
	}
	return payload[hdr+6 : hdr+6+int(n)]
}

// ParseFields retains the existing limits: 1 MiB and 4096 fields per buffer.
// Message schemas and the meaning of each field remain in the game services.
func ParseFields(b []byte) ([]Field, error) {
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("struct buffer too large")
	}
	var out []Field
	for i := 0; i < len(b); {
		tag, next, err := ReadVarint(b, i)
		if err != nil || tag>>3 == 0 {
			return nil, fmt.Errorf("bad protobuf tag")
		}
		i = next
		f := Field{Number: tag >> 3, Wire: tag & 7}
		switch f.Wire {
		case 0:
			f.Value, i, err = ReadVarint(b, i)
			if err != nil {
				return nil, err
			}
		case 2:
			n, nxt, e := ReadVarint(b, i)
			if e != nil || n > uint64(len(b)-nxt) {
				return nil, fmt.Errorf("truncated protobuf")
			}
			i = nxt
			f.Data = b[i : i+int(n)]
			i += int(n)
		default:
			return nil, fmt.Errorf("unsupported struct wire type")
		}
		out = append(out, f)
		if len(out) > 4096 {
			return nil, fmt.Errorf("too many fields")
		}
	}
	return out, nil
}
