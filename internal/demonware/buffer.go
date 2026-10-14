package demonware

import (
	"encoding/binary"
	"errors"
)

// Typed bdByteBuffer tags recovered from the clients. These are wire values,
// not service or game identifiers.
const (
	TagBool   = 0x01
	TagUint8  = 0x03
	TagUint16 = 0x06
	TagInt32  = 0x07
	TagUint32 = 0x08
	TagInt64  = 0x09
	TagUint64 = 0x0A
	TagString = 0x10
	TagBlob   = 0x13
	TagStruct = 0x17
)

// ErrShort reports a truncated wire value or envelope.
var ErrShort = errors.New("buffer too short")

// Writer builds a typed bdByteBuffer. Its zero value is ready to use.
// Bytes aliases the buffer until the next write; callers must not mutate it.
type Writer struct{ b []byte }

// NewWriter starts with an untyped prefix, such as a remote task header.
// The prefix is copied so subsequent writes never modify the caller's data.
func NewWriter(prefix []byte) *Writer { return &Writer{b: append([]byte(nil), prefix...)} }
func (w *Writer) Bytes() []byte       { return w.b }

// Reader borrows a typed argument buffer. The cursor is private; parsing
// methods check bounds before reading and never advance past the buffer.
// A failed read can consume its tag: discard the reader after an error.
// Remaining borrows the input; Blob and RequestTail return independent copies.
type Reader struct {
	b   []byte
	off int
}

func NewReader(b []byte) *Reader    { return &Reader{b: b} }
func (r *Reader) Position() int     { return r.off }
func (r *Reader) Remaining() []byte { return r.b[r.off:] }

// Int64 reads a tagged, fixed-width signed integer (not a protobuf ZigZag).
func (r *Reader) Int64() (int64, error) {
	if err := r.Tag(TagInt64); err != nil {
		return 0, err
	}
	if len(r.b)-r.off < 8 {
		return 0, ErrShort
	}
	v := int64(binary.LittleEndian.Uint64(r.b[r.off:]))
	r.off += 8
	return v, nil
}

// Bool consumes the legacy typed boolean; any nonzero byte is true.
func (r *Reader) Bool() (bool, error) {
	if err := r.Tag(TagBool); err != nil {
		return false, err
	}
	if r.off == len(r.b) {
		return false, ErrShort
	}
	v := r.b[r.off] != 0
	r.off++
	return v, nil
}
func (w *Writer) Uint8(v byte) { w.b = append(w.b, TagUint8, v) }

func (w *Writer) String(s string) {
	w.b = append(w.b, TagString)
	w.b = append(w.b, s...)
	w.b = append(w.b, 0)
}

func (w *Writer) Bool(v bool) {
	b := byte(0)
	if v {
		b = 1
	}
	w.b = append(w.b, TagBool, b)
}

func (w *Writer) Uint32(v uint32) {
	w.b = append(w.b, TagUint32)
	w.b = binary.LittleEndian.AppendUint32(w.b, v)
}

func (w *Writer) Uint64(v uint64) {
	w.b = append(w.b, TagUint64)
	w.b = binary.LittleEndian.AppendUint64(w.b, v)
}

func (w *Writer) RawUint64(v uint64) {
	w.b = binary.LittleEndian.AppendUint64(w.b, v)
}

func (w *Writer) Blob(p []byte) {
	w.b = append(w.b, TagBlob)
	w.Uint32(uint32(len(p)))
	w.b = append(w.b, p...)
}

func (w *Writer) Struct(p []byte) {
	w.b = append(w.b, TagStruct)
	w.Uint32(uint32(len(p)))
	w.b = append(w.b, p...)
}

func (r *Reader) Tag(want byte) error {
	if r.off >= len(r.b) {
		return ErrShort
	}
	if r.b[r.off] != want {
		return errors.New("unexpected tag")
	}
	r.off++
	return nil
}

func (r *Reader) Uint8() (byte, error) {
	if err := r.Tag(TagUint8); err != nil {
		return 0, err
	}
	if r.off >= len(r.b) {
		return 0, ErrShort
	}
	v := r.b[r.off]
	r.off++
	return v, nil
}

func (r *Reader) String() (string, error) {
	if err := r.Tag(TagString); err != nil {
		return "", err
	}
	end := r.off
	for end < len(r.b) && r.b[end] != 0 {
		end++
	}
	if end >= len(r.b) {
		return "", ErrShort
	}
	s := string(r.b[r.off:end])
	r.off = end + 1
	return s, nil
}

func (r *Reader) Uint32() (uint32, error) {
	if err := r.Tag(TagUint32); err != nil {
		return 0, err
	}
	if r.off+4 > len(r.b) {
		return 0, ErrShort
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v, nil
}

func (r *Reader) Blob() ([]byte, error) {
	if err := r.Tag(TagBlob); err != nil {
		return nil, err
	}
	n, err := r.Uint32()
	if err != nil {
		return nil, err
	}
	if uint64(n) > uint64(len(r.b)-r.off) {
		return nil, ErrShort
	}
	v := append([]byte{}, r.b[r.off:r.off+int(n)]...)
	r.off += int(n)
	return v, nil
}

// RequestTail copies the remaining request bytes, removing one terminal null.
// It does not advance the cursor. Terminator validation is service-specific.
func (r *Reader) RequestTail() []byte {
	v := r.b[r.off:]
	if len(v) > 0 && v[len(v)-1] == 0 {
		v = v[:len(v)-1]
	}
	return append([]byte{}, v...)
}

func (w *Writer) Raw(p []byte) { w.b = append(w.b, p...) }

func (r *Reader) Uint64() (uint64, error) {
	if err := r.Tag(TagUint64); err != nil {
		return 0, err
	}
	if r.off+8 > len(r.b) {
		return 0, ErrShort
	}
	v := binary.LittleEndian.Uint64(r.b[r.off:])
	r.off += 8
	return v, nil
}

// Uint64s reads typed uint64 values and legacy uint64 arrays (tag 0x6e).
// It retains the client's permissive lookup behavior: a truncated or unknown
// tail stops parsing and returns the complete values read so far.
func (r *Reader) Uint64s() []uint64 {
	var out []uint64
	for r.off < len(r.b) {
		switch r.b[r.off] {
		case TagUint64:
			if r.off+9 > len(r.b) {
				return out
			}
			out = append(out, binary.LittleEndian.Uint64(r.b[r.off+1:]))
			r.off += 9
		case 100 + TagUint64:
			r.off++
			if _, err := r.Uint32(); err != nil || r.off+4 > len(r.b) {
				return out
			}
			n := binary.LittleEndian.Uint32(r.b[r.off:])
			r.off += 4
			for i := uint32(0); i < n && r.off+8 <= len(r.b); i++ {
				out = append(out, binary.LittleEndian.Uint64(r.b[r.off:]))
				r.off += 8
			}
		default:
			return out
		}
	}
	return out
}

func (w *Writer) Uint16(v uint16) {
	w.b = append(w.b, TagUint16)
	w.b = binary.LittleEndian.AppendUint16(w.b, v)
}

func (w *Writer) Int64(v int64) {
	w.b = append(w.b, TagInt64)
	w.b = binary.LittleEndian.AppendUint64(w.b, uint64(v))
}
