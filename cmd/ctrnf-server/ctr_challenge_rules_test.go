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
