package main

import (
	"encoding/binary"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

// request builders in the documented layout
func rpStr(s string) []byte { return append(append([]byte{demonware.TagString}, s...), 0) }

func rpU64(v uint64) []byte {
	return binary.LittleEndian.AppendUint64([]byte{demonware.TagUint64}, v)
}

func rpU32(v uint32) []byte {
	return binary.LittleEndian.AppendUint32([]byte{demonware.TagUint32}, v)
}

func rpBlob(b []byte) []byte {
	return append(append([]byte{demonware.TagBlob}, rpU32(uint32(len(b)))...), b...)
}

func rpSetRequest(ctx string, id uint64, provider string, flag byte, data []byte) []byte {
	out := rpStr(ctx)
	out = append(out, rpU64(id)...)
	out = append(out, rpStr(provider)...)
	out = append(out, demonware.TagUint8, flag)
	return append(out, rpBlob(data)...)
}

func rpGetRequest(ctx string, ids ...uint64) []byte {
	out := append(rpStr(ctx), rpU32(uint32(len(ids)))...)
	for _, id := range ids {
		out = append(append(out, rpU64(id)...), rpStr("nx")...)
	}
	return out
}

// parse a get reply: transaction, error, task, count, [total, rows]
func rpRows(t *testing.T, reply []byte) (rows []presence.RichPresence, ids []uint64) {
	t.Helper()
	r := demonware.NewReader(reply)
	if _, err := r.Uint64(); err != nil {
		t.Fatal(err)
	}
	if code, err := r.Uint32(); err != nil || code != 0 {
		t.Fatalf("error code %d %v", code, err)
	}
	if _, err := r.Uint8(); err != nil {
		t.Fatal(err)
	}
	n, err := r.Uint32()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return nil, nil
	}
	if _, err := r.Uint32(); err != nil { // total
		t.Fatal(err)
	}
	for i := uint32(0); i < n; i++ {
		id, e1 := r.Uint64()
		plat, e2 := r.String()
		flag, e3 := r.Uint8()
		data, e4 := r.Blob()
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			t.Fatalf("row %d unreadable", i)
		}
		rows, ids = append(rows, presence.RichPresence{Platform: plat, Flag: flag, Data: data}), append(ids, id)
	}
	return rows, ids
}
