package main

import (
	"sort"
	"strconv"
)

// All calls hold the matchmaker lock. Reserve seats and ask the destination
// host to acknowledge them before sending CTR's native merge event (2008).
func mergeOpen(r *ctrRoom) bool {
	open, _ := r.hostDoc["lobby_open"].(bool)
	return open && r.connected() == r.occupancy() && r.occupancy() > 0
}

// Older host documents omit these fields. Preserve their existing behavior,
// but honor each explicit capability independently when a client supplies it.
func mergeEligible(r *ctrRoom, field string) bool {
	rules, _ := r.hostDoc["ruleset_payload"].(map[string]any)
	v, present := rules[field]
	if !present {
		return true
	}
	allowed, _ := v.(bool)
	return allowed
}

func (m *ctrMatchmaker) cancelMerge(dst *ctrRoom) []ctrNotice {
	src := dst.mergeSource
	if src == nil {
		return nil
	}
	dst.mergeSource = nil
	src.mergeTarget = 0
	dst.version++
	dst.hostDoc["update_id"] = dst.version
	dst.host.conn.Logf("CTR merge cancelled source=%d destination=%d", src.id, dst.id)
	return []ctrNotice{dst.notice(dst.host, ctrUpdateLobby)}
}

// Membership changes invalidate the reservation acknowledged by the host.
// The adapter calls this under the matchmaker lock before a departure/retry.
func (m *ctrMatchmaker) cancelRoomMerge(r *ctrRoom) []ctrNotice {
	if r.mergeSource != nil {
		return m.cancelMerge(r)
	}
	if dst := m.rooms[r.mergeTarget]; dst != nil && dst.mergeSource == r {
		return m.cancelMerge(dst)
	}
	return nil
}

func disjointRooms(a, b *ctrRoom) bool {
	seen := map[uint64]bool{}
	for _, p := range a.players {
		for _, pid := range p.doc.Members {
			seen[pid] = true
		}
	}
	for _, p := range b.players {
		for _, pid := range p.doc.Members {
			if seen[pid] {
				return false
			}
		}
	}
	return true
}

func (m *ctrMatchmaker) startMerge(dst *ctrRoom) []ctrNotice {
	if dst.mergeTarget != 0 && m.rooms[dst.mergeTarget] == nil {
		dst.mergeTarget = 0
	}
	if dst.mergeTarget != 0 || dst.mergeSource != nil || !mergeOpen(dst) || !mergeEligible(dst, "is_merge_dst") {
		return nil
	}
	candidates := []*ctrRoom{}
	for _, src := range m.rooms {
		if src == dst || src.mergeTarget != 0 || src.mergeSource != nil || !mergeOpen(src) || !mergeEligible(src, "is_merge_src") || src.host.filter != dst.host.filter {
			continue
		}
		// A stable direction avoids reciprocal merges and moves fewer players.
		if mergeEligible(src, "is_merge_dst") && mergeEligible(dst, "is_merge_src") &&
			(src.occupancy() > dst.occupancy() || (src.occupancy() == dst.occupancy() && src.id < dst.id)) {
			continue
		}
		if src.occupancy()+dst.occupancy() > min(src.capacity, dst.capacity) || !disjointRooms(src, dst) {
			continue
		}
		candidates = append(candidates, src)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].occupancy() != candidates[j].occupancy() {
			return candidates[i].occupancy() > candidates[j].occupancy()
		}
		return candidates[i].id < candidates[j].id
	})
	if len(candidates) == 0 {
		return nil
	}
	src := candidates[0]
	dst.mergeSource = src
	src.mergeTarget = dst.id
	if states, ok := dst.hostDoc["player_state"].(map[string]any); ok {
		for _, p := range src.players {
			for _, pid := range p.doc.Members {
				delete(states, strconv.FormatUint(pid, 10))
			}
		}
	}
	dst.version++
	dst.hostDoc["update_id"] = dst.version
	dst.host.conn.Logf("CTR merge reserved source=%d destination=%d incoming=%d total=%d version=%d", src.id, dst.id, src.occupancy(), dst.occupancy(), dst.version)
	return []ctrNotice{dst.notice(dst.host, ctrUpdateLobby)}
}

func (m *ctrMatchmaker) finishMerge(dst *ctrRoom) []ctrNotice {
	src := dst.mergeSource
	if src == nil {
		return nil
	}
	open, _ := dst.hostDoc["lobby_open"].(bool)
	seats := 0
	for _, p := range dst.players {
		seats += len(p.doc.Members)
	}
	valid := m.rooms[src.id] == src && src.mergeTarget == dst.id && open &&
		dst.connected() == seats && mergeOpen(src) && disjointRooms(dst, src) &&
		mergeEligible(dst, "is_merge_dst") && mergeEligible(src, "is_merge_src") &&
		dst.occupancy() <= min(dst.capacity, src.capacity)
	for _, p := range src.players {
		for _, pid := range p.doc.Members {
			if dst.state(pid) == 3 {
				valid = false
			}
		}
	}
	if !valid {
		return m.cancelMerge(dst)
	}
	dst.mergeSource = nil
	dst.capacity = min(dst.capacity, src.capacity)
	dst.players = append(dst.players, src.players...)
	delete(m.rooms, src.id)
	h, b := dst.documents()
	out := make([]ctrNotice, 0, len(src.players))
	for _, p := range src.players {
		// Verified msgMergeIntoLobby checks the second u64 against the current
		// lobby ID and passes the third to joinLobby; the first is the search ID.
		out = append(out, ctrNotice{p.conn, ctrMergeLobby, []uint64{p.id, src.id, dst.id}, h, b})
		dst.notified[p.conn.ConnID()] = true
	}
	dst.host.conn.Logf("CTR merge dispatched source=%d destination=%d searches=%d total=%d", src.id, dst.id, len(src.players), dst.occupancy())
	return out
}
