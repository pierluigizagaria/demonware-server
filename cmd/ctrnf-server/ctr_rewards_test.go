package main

import (
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
)

func capturedRace(at time.Time) ctr.AchievementEvent {
	return ctr.AchievementEvent{Name: "end_race", Timestamp: uint64(at.Unix()), Values: map[string]uint64{"Track": 3273086243, "RaceMode": 1, "Rank": 2, "CompletedLap": 3, "Time": 114, "Online": 0, "RewardGoldenWumpa": 0}}
}
