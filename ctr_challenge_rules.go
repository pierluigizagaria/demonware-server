package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

// Original Switch 1.0.15 AchievementMetadata. Dates are offsets into the
// same 84-day rotation used by the client; X/Y are numeric requirements,
// while equipment and track predicates use the client's seed-4 FNV hash.
//
//go:embed ctr-challenge-rules.json
var challengeRulesJSON []byte

type challengePeriod struct {
	Start, End int64
	Target     uint64
	X, Y       float64
	Filters    map[string][]string
}
type challengeRule struct {
	Name     string
	ID, Kind uint64
	Reward   int64
	Periods  []challengePeriod
}
type challengeSchedule struct {
	CycleStart   int64 `json:"cycle_start"`
	CycleSeconds int64 `json:"cycle_seconds"`
	Rules        []challengeRule
}

var challengeRules = func() challengeSchedule {
	var s challengeSchedule
	if err := json.Unmarshal(challengeRulesJSON, &s); err != nil {
		panic(err)
	}
	if s.CycleSeconds <= 0 {
		panic("invalid challenge cycle")
	}
	return s
}()

func challengeNameHash(s string) uint64 {
	v := uint32(4)
	for _, b := range []byte(s) {
		v = (v ^ uint32(b)) * 16777619
	}
	return uint64(v)
}

func (r challengeRule) current(a *economyAccount, now time.Time) (ctrAchievementState, challengePeriod, bool) {
	delta := now.Unix() - challengeRules.CycleStart
	if delta < 0 {
		return ctrAchievementState{}, challengePeriod{}, false
	}
	cycle := challengeRules.CycleStart + delta/challengeRules.CycleSeconds*challengeRules.CycleSeconds
	for _, p := range r.Periods {
		if now.Unix() < cycle+p.Start || now.Unix() >= cycle+p.End {
			continue
		}
		state := ctrAchievementState{Name: r.Name, ID: r.ID, Kind: r.Kind, Target: p.Target, Status: 2, Start: uint64(cycle + p.Start), End: uint64(cycle + p.End)}
		c := a.Challenges[challengeKey(state)]
		state.Progress = c.Progress
		if c.Completed != 0 {
			state.Progress = p.Target
			state.Status = 3
			state.Completed = c.Completed
		}
		return state, p, true
	}
	return ctrAchievementState{}, challengePeriod{}, false
}
func challengeKey(s ctrAchievementState) string { return fmt.Sprintf("%s:%d", s.Name, s.Start) }

func (r challengeRule) accepts(p challengePeriod, e achievementEvent) bool {
	if r.Name != e.Name {
		return false
	}
	for field, allowed := range p.Filters {
		got, exists := e.Values[field]
		if !exists {
			return false
		}
		matched := false
		for _, value := range allowed {
			if got == challengeNameHash(value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if p.X != 0 {
		x, ok := e.Values["X"]
		if !ok {
			return false
		}
		switch r.Name {
		case "finish_TRACKNAME_in_less_than_X_min", "do_a_lap_under_X_in_TRACKNAME":
			// The client reports whole seconds (despite the former event's name).
			if x == 0 || float64(x) >= p.X {
				return false
			}
		default:
			if float64(x) < p.X {
				return false
			}
		}
	}
	if p.Y != 0 {
		y, ok := e.Values["Y"]
		if !ok || float64(y) < p.Y {
			return false
		}
	}
	return true
}

const battleChallenge = "win_game_in_T_different_battles_TRACKNAME"

// challengeReceipt identifies an occurrence by what its rule reads: the event
// name and time, the effective increment, and the filter, X and Y values the
// period checks (X is also the battle mode). An ID or other field the rule
// ignores cannot make a replay a new occurrence. The values remain the
// client's statement; identical events in one report still count separately.
func challengeReceipt(p challengePeriod, e achievementEvent, increment uint64, battle bool) string {
	keys := map[string]bool{}
	for field := range p.Filters {
		keys[field] = true
	}
	keys["X"] = p.X != 0 || battle
	keys["Y"] = p.Y != 0
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|%d", e.Name, e.Timestamp, increment)
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if keys[k] {
			fmt.Fprintf(&b, "|%s=%d", k, e.Values[k])
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
}

// Completion, partial progress, retry receipts and coins share one atomic
// account save. Identical events in one report are separate occurrences;
// replaying that report after reconnecting must not advance them again.
func (s *economyStore) awardChallenges(pid uint64, events []achievementEvent, now time.Time) (int64, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.load(pid)
	if err != nil {
		return 0, nil, err
	}
	if a.Challenges == nil {
		a.Challenges = map[string]challengeCompletion{}
	}
	var credit int64
	var updates []byte
	changed := false
	for _, r := range challengeRules.Rules {
		state, period, active := r.current(a, now)
		if !active || state.Completed != 0 {
			continue
		}
		key := challengeKey(state)
		c := a.Challenges[key]
		if c.Receipts == nil {
			c.Receipts = map[string]uint64{}
		}
		occurrences := map[string]uint64{}
		before := c.Progress
		recordChanged := false
		for _, e := range events {
			if e.Timestamp < state.Start || e.Timestamp >= state.End || e.Timestamp > uint64(now.Add(5*time.Minute).Unix()) || !r.accepts(period, e) {
				continue
			}
			increment := e.Values["Progress"]
			if r.Name == "visit_pit_stop" {
				increment = e.Values["value"]
			}
			if increment == 0 {
				continue
			}
			// X is the client's battle-mode discriminator, not an additive win
			// count; an invalid or already counted mode records nothing.
			battle := r.Name == battleChallenge
			mode, hasMode := e.Values["X"]
			if battle && (!hasMode || mode < 9 || mode > 13 || c.Distinct[fmt.Sprint(mode)]) {
				continue
			}
			receipt := challengeReceipt(period, e, increment, battle)
			occurrences[receipt]++
			if c.Receipts[receipt] >= occurrences[receipt] {
				continue
			}
			c.Receipts[receipt] = occurrences[receipt]
			changed = true
			recordChanged = true
			if battle {
				if c.Distinct == nil {
					c.Distinct = map[string]bool{}
				}
				c.Distinct[fmt.Sprint(mode)] = true
				increment = 1
			}
			if increment >= state.Target-c.Progress {
				c.Progress = state.Target
				c.Completed = e.Timestamp
				credit += r.Reward
				c.Receipts = nil
				break
			}
			c.Progress += increment
		}
		if c.Progress != before {
			a.Challenges[key] = c
			updated, _, _ := r.current(a, now)
			updates = demonware.AppendBytesField(updates, 1, updated.encode())
		} else if recordChanged {
			a.Challenges[key] = c
		}
	}
	if !changed {
		return 0, nil, nil
	}
	a.Balances[wumpaCurrency] += credit
	if err := s.saveChange(pid, a, economyChange{source: "challenge reward"}); err != nil {
		return 0, nil, err
	}
	return credit, updates, nil
}
