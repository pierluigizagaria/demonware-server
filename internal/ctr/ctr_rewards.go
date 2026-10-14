package ctr

import (
	sha256 "crypto/sha256"
	_ "embed"
	json "encoding/json"
	fmt "fmt"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	time "time"
)

// Extracted from the Switch 1.0.15 CSystemData::gpMapData table, not a
// replacement flat rate. Keys use the client's seed-4 FNV track-name hash.
// PS4 01.21 systemData (update2.pak) has identical payouts for all 39 tracks
// and adds the PS4-only t155_sony_stadium, taken from that data.
//
//go:embed ctr-race-rates.json
var raceRatesJSON []byte

type raceRate struct {
	Track     string   `json:"track"`
	Positions [4]int64 `json:"positions"`
}

var raceRates = func() map[uint64]raceRate {
	m := map[uint64]raceRate{}
	if err := json.Unmarshal(raceRatesJSON, &m); err != nil {
		panic(err)
	}
	return m
}()

type AchievementEvent struct {
	Name      string
	Timestamp uint64
	ID        uint64
	Values    map[string]uint64
}

func BalanceUpdatePayload(pid uint64, amount int64) []byte {
	owner := demonwarepkg.AppendVarintField(nil, 1, pid)
	owner = demonwarepkg.AppendBytesField(owner, 2, []byte(ps4pkg.MarketplaceAccountType))
	entry := demonwarepkg.AppendBytesField(nil, 1, owner)
	entry = demonwarepkg.AppendVarintField(entry, 2, WumpaCurrency)
	entry = demonwarepkg.AppendVarintField(entry, 3, 0xffffffff) // legacy 32-bit balance sentinel
	entry = demonwarepkg.AppendVarintField(entry, 4, 0)
	entry = demonwarepkg.AppendVarintField(entry, 5, demonwarepkg.ZigZag(amount))
	return demonwarepkg.AppendBytesField(nil, 1, entry)
}

func (h *EconomyHandler) PushCurrentBalance(peer lobbypkg.TaskSession) {
	a, err := h.Economy.Snapshot(peer.PID())
	if err != nil {
		peer.Logf("CTR balance push read: %v", err)
		return
	}
	if err := peer.SendPush(76, func(w *demonwarepkg.Writer) { w.Struct(BalanceUpdatePayload(peer.PID(), a.Balances[10])) }); err != nil {
		peer.Logf("CTR balance push: %v", err)
	}
}

func ParseRewardEvents(payload []byte) ([]AchievementEvent, error) {
	sb := demonwarepkg.TaskStructPayload(payload)
	if sb == nil {
		return nil, fmt.Errorf("missing or truncated event struct")
	}
	fields, err := demonwarepkg.ParseFields(sb)
	if err != nil {
		return nil, err
	}
	var out []AchievementEvent
	for _, f := range fields {
		if f.Number != 2 || f.Wire != 2 {
			continue
		}
		parts, err := demonwarepkg.ParseFields(f.Data)
		if err != nil {
			return nil, err
		}
		e := AchievementEvent{Values: map[string]uint64{}}
		for _, p := range parts {
			switch p.Number {
			case 1:
				e.Name = string(p.Data)
			case 2:
				e.Timestamp = p.Value
			case 4:
				e.ID = p.Value
			case 3:
				kv, err := demonwarepkg.ParseFields(p.Data)
				if err != nil {
					return nil, err
				}
				var key string
				var value uint64
				for _, k := range kv {
					if k.Number == 1 {
						key = string(k.Data)
					}
					if k.Number == 2 {
						value = k.Value
					}
				}
				if key == "" || len(key) > 100 {
					return nil, fmt.Errorf("invalid event key")
				}
				if _, ok := e.Values[key]; ok {
					return nil, fmt.Errorf("duplicate event key")
				}
				e.Values[key] = value
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// raceReceipt identifies a race reward claim by exactly the values the payout
// uses, so an ID or extra field the reward ignores cannot make a retry a new
// claim. Those values are the client's own statement: a different time, rank
// or mode is a different claim, and nothing here proves the race took place.
func RaceReceipt(e AchievementEvent) string {
	v := e.Values
	mode15, online := v["RaceMode"] == 15, v["Online"] != 0
	var laps, seconds uint64
	if mode15 {
		laps = min(v["CompletedLap"], 8)
	}
	if online {
		seconds = v["Time"]
	}
	claim := fmt.Sprintf("end_race|%d|%d|%d|%t|%d|%t|%d|%t", e.Timestamp, v["Track"], min(v["Rank"], 4),
		mode15, laps, online, seconds, v["RewardGoldenWumpa"] == 1)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(claim)))
}

func (s *EconomyStore) AwardRaces(pid uint64, events []AchievementEvent, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.Load(pid)
	if err != nil {
		return 0, err
	}
	if a.RaceReceipts == nil {
		a.RaceReceipts = map[string]bool{}
	}
	if a.OnlineSeconds == nil {
		a.OnlineSeconds = map[string]uint64{}
	}
	var total int64
	for _, e := range events {
		if e.Name != "end_race" {
			continue
		}
		rate, ok := raceRates[e.Values["Track"]]
		if !ok {
			continue
		}
		rank, seconds, laps := e.Values["Rank"], e.Values["Time"], e.Values["CompletedLap"]
		if rank < 1 || rank > 8 || seconds == 0 || seconds > 86400 || laps == 0 || laps > 100 || e.Timestamp == 0 {
			continue
		}
		tm := time.Unix(int64(e.Timestamp), 0).UTC()
		if tm.After(now.Add(5*time.Minute)) || tm.Before(now.Add(-7*24*time.Hour)) {
			continue
		}
		// Retries must not grant the same race twice, including across restarts.
		receipt := RaceReceipt(e)
		if a.RaceReceipts[receipt] {
			continue
		}
		pos := rank - 1
		if pos > 3 {
			pos = 3
		}
		amount := rate.Positions[pos]
		if e.Values["RaceMode"] == 15 {
			n := laps
			if n > 8 {
				n = 8
			}
			amount = amount * int64(n) / 3
		}
		if e.Values["Online"] != 0 {
			day := tm.Format("2006-01-02")
			if a.OnlineSeconds[day] < 1800 {
				amount *= 5
			}
			a.OnlineSeconds[day] += seconds
		}
		// CNetworkConfig defaults: Friday 17:00 UTC through Monday 08:00 UTC.
		// The client applies the weekend multiplier to local races as well.
		startDay := tm.Add(-17 * time.Hour).Weekday()
		endDay := tm.Add(-8 * time.Hour).Weekday()
		if startDay == time.Friday || startDay == time.Saturday || endDay == time.Sunday {
			amount *= 2
		}
		// Golden Wumpa is a fixed 200 added after race multipliers.
		if e.Values["RewardGoldenWumpa"] == 1 {
			amount += 200
		}
		a.RaceReceipts[receipt] = true
		a.Balances[WumpaCurrency] += amount
		total += amount
	}
	if total != 0 {
		if err := s.SaveChange(pid, a, EconomyChange{Source: "race reward"}); err != nil {
			return 0, err
		}
	}
	return total, nil
}
