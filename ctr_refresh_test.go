package main

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

func TestInventoryCompleteClientWireLayout(t *testing.T) {
	w := &demonware.Writer{}
	writeInventoryItem(w, 42, 688)
	// Independent bdMarketplaceInventory::deserialize wire fixture. In particular,
	// signed int64 uses 09; 14 is the MAX_INT64 sentinel with no trailing bytes.
	want, err := hex.DecodeString("0a2a000000000000001070736e0008b00200000801000000080000000013080000000008000000000900000000000000000600000800000000")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("inventory wire\ngot %x\nwant %x", w.Bytes(), want)
	}
}

func TestPitStopRefreshRetryRestartLimitAndReset(t *testing.T) {
	s := &economyStore{initialBalance: func() int64 { return testStartBalance }, dir: t.TempDir()}
	now := time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)
	e := achievementEvent{Name: "pitstop_daily_refresh", Timestamp: uint64(now.Unix()), Values: map[string]uint64{"value": 1}}
	b, err := s.reportPitStopRefresh(42, []achievementEvent{e, e}, now)
	if err != nil || len(b) == 0 {
		t.Fatalf("%x %v", b, err)
	}
	s = &economyStore{initialBalance: func() int64 { return testStartBalance }, dir: s.dir}
	s.reportPitStopRefresh(42, []achievementEvent{e}, now)
	a, _ := s.snapshot(42)
	if got := currentPitStopRefresh(a, now); got.Progress != 1 || got.Status != 2 {
		t.Fatalf("retry %+v", got)
	}
	e.Timestamp++
	s.reportPitStopRefresh(42, []achievementEvent{e}, now.Add(time.Second))
	e.Timestamp++
	s.reportPitStopRefresh(42, []achievementEvent{e}, now.Add(2*time.Second))
	a, _ = s.snapshot(42)
	if got := currentPitStopRefresh(a, now); got.Progress != 2 || got.Status != 3 {
		t.Fatalf("limit %+v", got)
	}
	reset := time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC)
	if got := currentPitStopRefresh(a, reset); got.Progress != 0 {
		t.Fatalf("reset %+v", got)
	}
	b, err = s.reportPitStopRefresh(42, []achievementEvent{e}, reset)
	if err != nil || len(b) != 0 {
		t.Fatalf("stale refresh accepted: %x %v", b, err)
	}
}

func TestRefreshNotificationCanBeDecodedAndNamesRealCounter(t *testing.T) {
	s := currentPitStopRefresh(&economyAccount{}, time.Now())
	f, err := demonware.ParseFields(s.encode())
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint64]demonware.Field{}
	for _, v := range f {
		got[v.Number] = v
	}
	if string(got[1].Data) != "pitstop_daily_refresh" || got[2].Value != 1 {
		t.Fatal("wrong refresh callback")
	}
	for _, tag := range []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 11} {
		if _, ok := got[tag]; !ok {
			t.Fatalf("missing required field %d", tag)
		}
	}
	if got[5].Value < 1 || got[5].Value > 5 {
		t.Fatal("client rejects invalid achievement status")
	}
	w := demonware.NewWriter([]byte{125, 3, 3})
	w.Struct(demonware.AppendVarintField(nil, 2, 3))
	if achievementRequested(w.Bytes(), s) {
		t.Fatal("refresh included in daily challenge query")
	}
	w = demonware.NewWriter([]byte{125, 3, 3})
	w.Struct(demonware.AppendVarintField(nil, 2, 1))
	if !achievementRequested(w.Bytes(), s) {
		t.Fatal("refresh missing from progression query")
	}
}
