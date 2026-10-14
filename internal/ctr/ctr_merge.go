package ctr

import (
	sort "sort"
	strconv "strconv"
)

// All calls hold the matchmaker lock. Reserve seats and ask the destination
// host to acknowledge them before sending CTR's native merge event (2008).
func mergeOpen(r *CtrRoom) bool {
	open, _ := r.HostDoc["lobby_open"].(bool)
	return open && r.connected() == r.Occupancy() && r.Occupancy() > 0
}

// Older host documents omit these fields. Preserve their existing behavior,
// but honor each explicit capability independently when a client supplies it.
func mergeEligible(r *CtrRoom, field string) bool {
	rules, _ := r.HostDoc["ruleset_payload"].(map[string]any)
	v, present := rules[field]
	if !present {
		return true
	}
	allowed, _ := v.(bool)
	return allowed
}

func (m *CtrMatchmaker) cancelMerge(dst *CtrRoom) []ctrNotice {
	src := dst.MergeSource
	if src == nil {
		return nil
	}
	dst.MergeSource = nil
	src.MergeTarget = 0
	dst.Version++
	dst.HostDoc["update_id"] = dst.Version
	dst.Host.Conn.Logf("[ctr] merge cancelled source=%d destination=%d", src.Id, dst.Id)
	return []ctrNotice{dst.notice(dst.Host, CtrUpdateLobby)}
}

// Membership changes invalidate the reservation acknowledged by the host.
// The adapter calls this under the matchmaker lock before a departure/retry.
func (m *CtrMatchmaker) cancelRoomMerge(r *CtrRoom) []ctrNotice {
	if r.MergeSource != nil {
		return m.cancelMerge(r)
	}
	if dst := m.Rooms[r.MergeTarget]; dst != nil && dst.MergeSource == r {
		return m.cancelMerge(dst)
	}
	return nil
}

func disjointRooms(a, b *CtrRoom) bool {
	seen := map[uint64]bool{}
	for _, p := range a.Players {
		for _, pid := range p.Doc.Members {
			seen[pid] = true
		}
	}
	for _, p := range b.Players {
		for _, pid := range p.Doc.Members {
			if seen[pid] {
				return false
			}
		}
	}
	return true
}

func (m *CtrMatchmaker) StartMerge(dst *CtrRoom) []ctrNotice {
	if dst.MergeTarget != 0 && m.Rooms[dst.MergeTarget] == nil {
		dst.MergeTarget = 0
	}
	if dst.MergeTarget != 0 || dst.MergeSource != nil || !mergeOpen(dst) || !mergeEligible(dst, "is_merge_dst") {
		return nil
	}
	candidates := []*CtrRoom{}
	for _, src := range m.Rooms {
		if src == dst || src.MergeTarget != 0 || src.MergeSource != nil || !mergeOpen(src) || !mergeEligible(src, "is_merge_src") || src.Host.Filter != dst.Host.Filter {
			continue
		}
		// A stable direction avoids reciprocal merges and moves fewer players.
		if mergeEligible(src, "is_merge_dst") && mergeEligible(dst, "is_merge_src") &&
			(src.Occupancy() > dst.Occupancy() || (src.Occupancy() == dst.Occupancy() && src.Id < dst.Id)) {
			continue
		}
		if src.Occupancy()+dst.Occupancy() > min(src.Capacity, dst.Capacity) || !disjointRooms(src, dst) {
			continue
		}
		candidates = append(candidates, src)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Occupancy() != candidates[j].Occupancy() {
			return candidates[i].Occupancy() > candidates[j].Occupancy()
		}
		return candidates[i].Id < candidates[j].Id
	})
	if len(candidates) == 0 {
		return nil
	}
	src := candidates[0]
	dst.MergeSource = src
	src.MergeTarget = dst.Id
	if states, ok := dst.HostDoc["player_state"].(map[string]any); ok {
		for _, p := range src.Players {
			for _, pid := range p.Doc.Members {
				delete(states, strconv.FormatUint(pid, 10))
			}
		}
	}
	dst.Version++
	dst.HostDoc["update_id"] = dst.Version
	dst.Host.Conn.Logf("[ctr] merge reserved source=%d destination=%d incoming=%d total=%d version=%d", src.Id, dst.Id, src.Occupancy(), dst.Occupancy(), dst.Version)
	return []ctrNotice{dst.notice(dst.Host, CtrUpdateLobby)}
}

func (m *CtrMatchmaker) finishMerge(dst *CtrRoom) []ctrNotice {
	src := dst.MergeSource
	if src == nil {
		return nil
	}
	open, _ := dst.HostDoc["lobby_open"].(bool)
	seats := 0
	for _, p := range dst.Players {
		seats += len(p.Doc.Members)
	}
	valid := m.Rooms[src.Id] == src && src.MergeTarget == dst.Id && open &&
		dst.connected() == seats && mergeOpen(src) && disjointRooms(dst, src) &&
		mergeEligible(dst, "is_merge_dst") && mergeEligible(src, "is_merge_src") &&
		dst.Occupancy() <= min(dst.Capacity, src.Capacity)
	for _, p := range src.Players {
		for _, pid := range p.Doc.Members {
			if dst.state(pid) == 3 {
				valid = false
			}
		}
	}
	if !valid {
		return m.cancelMerge(dst)
	}
	dst.MergeSource = nil
	dst.Capacity = min(dst.Capacity, src.Capacity)
	dst.Players = append(dst.Players, src.Players...)
	delete(m.Rooms, src.Id)
	h, b := dst.Documents()
	out := make([]ctrNotice, 0, len(src.Players))
	for _, p := range src.Players {
		// Verified msgMergeIntoLobby checks the second u64 against the current
		// lobby ID and passes the third to joinLobby; the first is the search ID.
		out = append(out, ctrNotice{p.Conn, CtrMergeLobby, []uint64{p.Id, src.Id, dst.Id}, h, b})
		dst.notified[p.Conn.ConnID()] = true
	}
	dst.Host.Conn.Logf("[ctr] merge dispatched source=%d destination=%d searches=%d total=%d", src.Id, dst.Id, len(src.Players), dst.Occupancy())
	return out
}
