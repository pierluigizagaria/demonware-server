package main

import (
	"testing"
	"time"
)

func capturedRace(at time.Time) achievementEvent {
	return achievementEvent{Name: "end_race", Timestamp: uint64(at.Unix()), Values: map[string]uint64{"Track": 3273086243, "RaceMode": 1, "Rank": 2, "CompletedLap": 3, "Time": 114, "Online": 0, "RewardGoldenWumpa": 0}}
}
func TestOriginalRaceRewardAndRestartRetry(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "10000")
	s := &economyStore{dir: t.TempDir()}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	e := capturedRace(now)
	amount, err := s.awardRaces(42, []achievementEvent{e}, now)
	if err != nil || amount != 27 {
		t.Fatalf("original Jungle Boogie 2nd payout: %d %v", amount, err)
	}
	s = &economyStore{dir: s.dir}
	amount, err = s.awardRaces(42, []achievementEvent{e}, now)
	if err != nil || amount != 0 {
		t.Fatal("retry awarded twice")
	}
	a, _ := s.snapshot(42)
	if a.Balances[10] != 10027 {
		t.Fatal("reward not persisted")
	}
}
func TestOriginalDailyBonusExpiresByRaceTime(t *testing.T) {
	s := &economyStore{dir: t.TempDir()}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for i, want := range []int64{135, 135, 27} {
		e := capturedRace(now.Add(time.Duration(i) * time.Second))
		e.Values["Online"] = 1
		e.Values["Time"] = 900
		got, err := s.awardRaces(42, []achievementEvent{e}, now.Add(time.Minute))
		if err != nil || got != want {
			t.Fatalf("race %d payout %d want %d (%v)", i, got, want, err)
		}
	}
}
func TestOriginalWeekendBoundaryAndGoldenBonus(t *testing.T) {
	for _, tc := range []struct {
		day, hour int
		want      int64
	}{{25, 16, 227}, {25, 17, 254}, {28, 7, 254}, {28, 8, 227}} {
		s := &economyStore{dir: t.TempDir()}
		now := time.Date(2026, 9, tc.day, tc.hour, 0, 0, 0, time.UTC)
		e := capturedRace(now)
		e.Values["RewardGoldenWumpa"] = 1
		got, err := s.awardRaces(42, []achievementEvent{e}, now)
		if err != nil || got != tc.want {
			t.Fatalf("%v: got %d want %d: %v", now, got, tc.want, err)
		}
	}
}
func TestRewardRejectsUnknownTrackAndDuplicateEvents(t *testing.T) {
	s := &economyStore{dir: t.TempDir()}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	e := capturedRace(now)
	e.Values["Track"] = 123
	got, err := s.awardRaces(42, []achievementEvent{e}, now)
	if err != nil || got != 0 {
		t.Fatal("unknown track paid guessed rate")
	}
	e = capturedRace(now)
	got, err = s.awardRaces(42, []achievementEvent{e, e}, now)
	if err != nil || got != 27 {
		t.Fatal("duplicate batch reward")
	}
}
func TestRewardEventWireRoundTrip(t *testing.T) {
	e := pbBytesField(nil, 1, []byte("end_race"))
	e = pbVarintField(e, 2, 1789989691)
	kv := pbBytesField(nil, 1, []byte("Track"))
	kv = pbVarintField(kv, 2, 3273086243)
	e = pbBytesField(e, 3, kv)
	sb := pbBytesField(nil, 1, []byte("octane_switch"))
	sb = pbBytesField(sb, 2, e)
	w := &bdWriter{}
	w.b = []byte{125, 3, 1}
	w.structv(sb)
	got, err := parseRewardEvents(w.b)
	if err != nil || len(got) != 1 || got[0].Values["Track"] != 3273086243 {
		t.Fatalf("decode %v %v", got, err)
	}
	w.b = w.b[:len(w.b)-1]
	if _, err := parseRewardEvents(w.b); err == nil {
		t.Fatal("truncated event accepted")
	}
}

func TestRaceReceiptIgnoresFieldsOutsideThePayout(t *testing.T) {
	t.Setenv("CTR_START_BALANCE", "0")
	s := &economyStore{dir: t.TempDir()}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	e := capturedRace(now)
	if amount, err := s.awardRaces(42, []achievementEvent{e}, now); err != nil || amount != 27 {
		t.Fatalf("first payout %d, %v", amount, err)
	}
	padded := capturedRace(now)
	padded.ID = 99
	padded.Values["Ignored"] = 1
	padded.Values["RaceMode"] = 2 // pays like mode 1
	padded.Values["Rank"] = 2
	padded.Values["Time"] = 115 // offline: time does not change the payout
	if amount, err := s.awardRaces(42, []achievementEvent{padded}, now); err != nil || amount != 0 {
		t.Fatalf("same claim with ignored fields paid again: %d, %v", amount, err)
	}
	other := capturedRace(now.Add(-time.Minute))
	if amount, err := s.awardRaces(42, []achievementEvent{other}, now); err != nil || amount != 27 {
		t.Fatalf("a different race was not paid: %d, %v", amount, err)
	}
}

// PS4 01.21 systemData (update2.pak) holds the same gpMapData payouts as the
// Switch table for all 39 shared tracks, plus the PS4-only Sony Stadium.
func TestPS4OnlyTrackHasOriginalPayout(t *testing.T) {
	rate, ok := raceRates[challengeNameHash("t155_sony_stadium")]
	if !ok || rate.Positions != [4]int64{45, 41, 38, 34} {
		t.Fatalf("Sony Stadium payout %+v", rate)
	}
}
