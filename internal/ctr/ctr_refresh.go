package ctr

import (
	json "encoding/json"
	demonwarepkg "github.com/pierluigizagaria/demonware-server/internal/demonware"
	time "time"
)

// Switch 1.0.15 AchievementMetadata, pitstop_daily_refresh parameters:
// daily periods begin at 15:00 UTC, progressTarget is 2. The callback copies
// achievement progress into the marketplace's rotation counter. PS4 01.21
// AchievementGrandPrixConfig (update2.pak) has the same 84 daily periods from
// 2020-03-26 15:00 with target 2 (the data has no time zone; UTC is assumed).
type PitStopRefreshRecord struct {
	Day      string          `json:"day"`
	Count    uint64          `json:"count"`
	Receipts map[string]bool `json:"receipts"`
}

type ctrAchievementState struct {
	ID, Completed                              uint64
	Name                                       string
	Kind, Progress, Target, Status, Start, End uint64
}

func (a ctrAchievementState) Encode() []byte {
	e := demonwarepkg.AppendBytesField(nil, 1, []byte(a.Name))
	for _, f := range []struct {
		tag   byte
		value uint64
	}{
		{2, a.Kind}, {3, a.Progress}, {4, a.Target}, {5, a.Status},
		{6, a.Completed}, {7, a.Start}, {8, a.End}, {9, uint64(boolToCompletion(a.Completed))}, {11, a.ID},
	} {
		e = demonwarepkg.AppendVarintField(e, f.tag, f.value)
	}
	return e
}

func pitStopDay(now time.Time) (string, uint64, uint64) {
	u := now.UTC().Add(-15 * time.Hour)
	start := time.Date(u.Year(), u.Month(), u.Day(), 15, 0, 0, 0, time.UTC)
	return start.Format("2006-01-02"), uint64(start.Unix()), uint64(start.Add(24 * time.Hour).Unix())
}

func CurrentPitStopRefresh(a *EconomyAccount, now time.Time) ctrAchievementState {
	day, start, end := pitStopDay(now)
	s := ctrAchievementState{Name: "pitstop_daily_refresh", ID: 255, Kind: 1, Target: 2, Status: 2, Start: start, End: end}
	if a.PitStopRefresh != nil && a.PitStopRefresh.Day == day {
		s.Progress = a.PitStopRefresh.Count
	}
	if s.Progress >= s.Target {
		s.Progress = s.Target
		s.Status = 3
	}
	return s
}

func (s *EconomyStore) ReportPitStopRefresh(pid uint64, events []AchievementEvent, now time.Time) ([]byte, error) {
	var requests []AchievementEvent
	for _, e := range events {
		if e.Name == "pitstop_daily_refresh" && e.Values["value"] == 1 {
			requests = append(requests, e)
		}
	}
	if len(requests) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.Load(pid)
	if err != nil {
		return nil, err
	}
	day, start, end := pitStopDay(now)
	if a.PitStopRefresh == nil || a.PitStopRefresh.Day != day {
		a.PitStopRefresh = &PitStopRefreshRecord{Day: day, Receipts: map[string]bool{}}
	}
	r := a.PitStopRefresh
	if r.Receipts == nil {
		r.Receipts = map[string]bool{}
	}
	valid := false
	for _, e := range requests {
		// A stale retry must not spend a refresh from a new daily period.
		if e.Timestamp < start || e.Timestamp >= end || e.Timestamp > uint64(now.Add(5*time.Minute).Unix()) {
			continue
		}
		valid = true
		key, _ := json.Marshal(e)
		if !r.Receipts[string(key)] && r.Count < 2 {
			r.Count++
			r.Receipts[string(key)] = true
		}
	}
	if !valid {
		return nil, nil
	}
	if err := s.SaveChange(pid, a, EconomyChange{Source: "pit stop refresh"}); err != nil {
		return nil, err
	}
	return demonwarepkg.AppendBytesField(nil, 1, CurrentPitStopRefresh(a, now).Encode()), nil
}

// bdGetAchievementStatesRequest: kinds=2, limit=3, token=4,
// statuses=5, names=6. Do not leak a progression state into daily challenges.
func AchievementRequested(payload []byte, s ctrAchievementState) bool {
	fields, err := demonwarepkg.ParseFields(demonwarepkg.TaskStructPayload(payload))
	if err != nil {
		return false
	}
	hasKind, kind, hasStatus, status, hasName, name := false, false, false, false, false, false
	for _, f := range fields {
		switch f.Number {
		case 2:
			hasKind = true
			kind = kind || f.Value == s.Kind
		case 3:
			if f.Value == 0 {
				return false
			}
		case 4:
			if len(f.Data) != 0 {
				return false
			}
		case 5:
			hasStatus = true
			status = status || f.Value == s.Status
		case 6:
			hasName = true
			name = name || string(f.Data) == s.Name
		case 7:
			return false // Explicit IDs are not used for this named counter.
		}
	}
	return (!hasKind || kind) && (!hasStatus || status) && (!hasName || name)
}

func boolToCompletion(timestamp uint64) uint32 {
	if timestamp != 0 {
		return 1
	}
	return 0
}
