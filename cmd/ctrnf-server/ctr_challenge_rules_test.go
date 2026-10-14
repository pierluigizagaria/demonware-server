package main

import (
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
)

func ruleForTest(t *testing.T, name string) ctr.ChallengeRule {
	t.Helper()
	for _, r := range ctr.ChallengeScheduleSnapshot().Rules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("missing rule %s", name)
	return ctr.ChallengeRule{}
}

func ruleTime(r ctr.ChallengeRule) time.Time {
	return time.Unix(ctr.ChallengeScheduleSnapshot().CycleStart+30*ctr.ChallengeScheduleSnapshot().CycleSeconds+r.Periods[0].Start+60, 0)
}

func ruleEvent(r ctr.ChallengeRule, now time.Time) ctr.AchievementEvent {
	p := r.Periods[0]
	e := ctr.AchievementEvent{Name: r.Name, Timestamp: uint64(now.Unix()), Values: map[string]uint64{"Progress": 1, "X": uint64(p.X), "Y": uint64(p.Y)}}
	for k, vs := range p.Filters {
		e.Values[k] = ctr.ChallengeNameHash(vs[0])
	}
	return e
}

func TestOriginalChallengeCatalog(t *testing.T) {
	if len(ctr.ChallengeScheduleSnapshot().Rules) != 59 {
		t.Fatal(len(ctr.ChallengeScheduleSnapshot().Rules))
	}
	for _, r := range ctr.ChallengeScheduleSnapshot().Rules {
		expected := map[uint64]int64{3: 25, 4: 75, 5: 200}[r.Kind]
		if r.Reward != expected || expected == 0 || len(r.Periods) == 0 {
			t.Fatalf("invalid %s", r.Name)
		}
		for _, p := range r.Periods {
			if p.Target == 0 || p.Start < 0 || p.End <= p.Start || p.End > ctr.ChallengeScheduleSnapshot().CycleSeconds {
				t.Fatalf("bad period %s %+v", r.Name, p)
			}
		}
	}
}

func TestDriftThresholdRewardAndRestart(t *testing.T) {
	setStartBalance(t, 10000)
	s := &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	r := ruleForTest(t, "drift_for_X_seconds_consecutively")
	now := ruleTime(r)
	e := ruleEvent(r, now)
	e.Values["X"] = 2
	if c, u, err := s.AwardChallenges(42, []ctr.AchievementEvent{e}, now); err != nil || c != 0 || len(u) != 0 {
		t.Fatalf("short drift accepted %d %v", c, err)
	}
	e.Values["X"] = 3
	if c, u, err := s.AwardChallenges(42, []ctr.AchievementEvent{e}, now); err != nil || c != 25 || len(u) == 0 {
		t.Fatalf("drift not rewarded %d %v", c, err)
	}
	s = &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: s.Dir}
	if c, u, err := s.AwardChallenges(42, []ctr.AchievementEvent{e}, now); err != nil || c != 0 || len(u) != 0 {
		t.Fatalf("duplicate completion %d %v", c, err)
	}
	a, _ := s.Snapshot(42)
	state, _, _ := r.Current(a, now)
	if a.Balances[10] != 10025 || state.Status != 3 || state.Progress != 1 {
		t.Fatalf("not persisted %+v", state)
	}
}

func TestWeeklyProgressCountsOccurrencesAndDeduplicatesReplay(t *testing.T) {
	s := &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	r := ruleForTest(t, "use_juiced_up_powerups_T_times")
	now := ruleTime(r)
	e := ruleEvent(r, now)
	batch := []ctr.AchievementEvent{e, e, e, e}
	c, u, err := s.AwardChallenges(42, batch, now)
	if err != nil || c != 0 || len(u) == 0 {
		t.Fatalf("partial progress %d %v", c, err)
	}
	s = &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: s.Dir}
	c, u, err = s.AwardChallenges(42, batch, now)
	if err != nil || c != 0 || len(u) != 0 {
		t.Fatal("replay advanced")
	}
	a, _ := s.Snapshot(42)
	st, _, _ := r.Current(a, now)
	if st.Progress != 4 || st.Status != 2 {
		t.Fatalf("partial state %+v", st)
	}
	e.Timestamp++
	e.Values["Progress"] = 6
	c, u, err = s.AwardChallenges(42, []ctr.AchievementEvent{e}, now.Add(time.Second))
	if err != nil || c != 75 || len(u) == 0 {
		t.Fatalf("weekly completion %d %v", c, err)
	}
	// The next weekly occurrence must not inherit completed progress.
	next := time.Unix(ctr.ChallengeScheduleSnapshot().CycleStart+30*ctr.ChallengeScheduleSnapshot().CycleSeconds+r.Periods[1].Start+60, 0)
	a, _ = s.Snapshot(42)
	st, _, active := r.Current(a, next)
	if !active || st.Progress != 0 || st.Completed != 0 {
		t.Fatalf("period leakage %+v", st)
	}
}

func TestChallengeFiltersAndProTimeDirection(t *testing.T) {
	r := ruleForTest(t, "do_a_lap_under_X_in_TRACKNAME")
	p := r.Periods[0]
	e := ruleEvent(r, ruleTime(r))
	if r.Accepts(p, e) {
		t.Fatal("equal threshold accepted for under")
	}
	e.Values["X"]--
	if !r.Accepts(p, e) {
		t.Fatal("qualifying lap rejected")
	}
	e.Values["Track"]++
	if r.Accepts(p, e) {
		t.Fatal("wrong track accepted")
	}
	r = ruleForTest(t, "win_mirror_mode_CHARACTER")
	p = r.Periods[0]
	e = ruleEvent(r, ruleTime(r))
	if !r.Accepts(p, e) {
		t.Fatal("original character rejected")
	}
	delete(e.Values, "Character")
	if r.Accepts(p, e) {
		t.Fatal("missing character accepted")
	}
}

func TestBattleChallengeRequiresDistinctModes(t *testing.T) {
	s := &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	r := ruleForTest(t, "win_game_in_T_different_battles_TRACKNAME")
	now := ruleTime(r)
	e := ruleEvent(r, now)
	for i, mode := range []uint64{9, 9, 10, 11} {
		e.Timestamp++
		e.Values["X"] = mode
		c, _, err := s.AwardChallenges(42, []ctr.AchievementEvent{e}, now.Add(10*time.Second))
		if err != nil || (i < 3 && c != 0) || (i == 3 && c != 75) {
			t.Fatalf("mode %d credit %d: %v", mode, c, err)
		}
	}
}

func TestChallengePeriodAndExistingCompletion(t *testing.T) {
	s := &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	r := ruleForTest(t, "visit_pit_stop")
	now := ruleTime(r)
	e := ruleEvent(r, now)
	e.Values["value"] = 1
	if c, _, err := s.AwardWindowShopping(42, []ctr.AchievementEvent{e}, now); err != nil || c != 25 {
		t.Fatal(c, err)
	}
	if c, u, err := s.AwardChallenges(42, []ctr.AchievementEvent{e}, now); err != nil || c != 0 || len(u) != 0 {
		t.Fatal("legacy completion reawarded")
	}
	e.Timestamp = uint64(now.Unix() - 86400)
	if c, u, err := s.AwardChallenges(43, []ctr.AchievementEvent{e}, now); err != nil || c != 0 || len(u) != 0 {
		t.Fatal("stale event accepted")
	}
}

func TestChallengeReceiptIgnoresFieldsOutsideTheRule(t *testing.T) {
	s := &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	r := ruleForTest(t, "use_juiced_up_powerups_T_times")
	now := ruleTime(r)
	if _, u, err := s.AwardChallenges(42, []ctr.AchievementEvent{ruleEvent(r, now)}, now); err != nil || len(u) == 0 {
		t.Fatalf("first occurrence not counted: %v", err)
	}
	padded := ruleEvent(r, now)
	padded.ID = 7
	padded.Values["Ignored"] = 1
	if _, u, err := s.AwardChallenges(42, []ctr.AchievementEvent{padded}, now); err != nil || len(u) != 0 {
		t.Fatal("replay with fields the rule ignores advanced progress")
	}
}

func TestInvalidBattleModeRecordsNothing(t *testing.T) {
	s := &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }, Dir: t.TempDir()}
	r := ruleForTest(t, ctr.BattleChallenge)
	now := ruleTime(r)
	e := ruleEvent(r, now)
	e.Values["X"] = 5
	if _, u, err := s.AwardChallenges(42, []ctr.AchievementEvent{e}, now); err != nil || len(u) != 0 {
		t.Fatal("invalid battle mode counted")
	}
	if a, err := s.Snapshot(42); err != nil || len(a.Challenges) != 0 {
		t.Fatalf("invalid battle mode stored a receipt: %+v", a.Challenges)
	}
}
