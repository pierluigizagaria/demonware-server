package ctr

import (
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
)

// Achievements use struct task replies; ordinary result-count replies fail decoding.
const svcAchievements = 125

// Push 75 carries named achievement states, not a generic acknowledgement.
const pushAchievementsUpdated = 75

const (
	taskReportEvents         = 1
	taskReportUserEvents     = 2
	TaskGetAchievementStates = 3
	TaskGetUserState         = 9
)

// onAchievements answers every achievements-engine task with a well formed struct.
func (h *EconomyHandler) OnAchievements(peer lobby.TaskSession, task byte, payload []byte) (result lobby.TaskResult) {
	switch task {
	case taskReportEvents, taskReportUserEvents:
		var credit int64
		var updates []byte
		if task == taskReportEvents {
			events, err := ParseRewardEvents(payload)
			if err != nil {
				peer.Logf("[ctr] reward event rejected: %v", err)
				result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
				return result
			}
			for _, e := range events {
				peer.Logf("[ctr] reward event=%s timestamp=%d values=%v", e.Name, e.Timestamp, e.Values)
			}
			credit, err = h.Economy.AwardRaces(peer.PID(), events, peer.Now())
			if err != nil {
				peer.Logf("[ctr] reward persistence failed: %v", err)
				result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
				return result
			}
			updates, err = h.Economy.ReportPitStopRefresh(peer.PID(), events, peer.Now())
			if err != nil {
				peer.Logf("[ctr] refresh persistence failed: %v", err)
				result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
				return result
			}
			challengeCredit, challengeUpdates, err := h.Economy.AwardChallenges(peer.PID(), events, peer.Now())
			if err != nil {
				peer.Logf("[ctr] challenge persistence failed: %v", err)
				result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
				return result
			}
			credit += challengeCredit
			updates = append(updates, challengeUpdates...)
			if challengeCredit != 0 {
				peer.Logf("[ctr] challenge reward=%d persisted", challengeCredit)
			}

			if credit != 0 {
				peer.Logf("[ctr] reward credit=%d currency=10 persisted", credit)
			}
		}
		result.AfterReply = func(_ error) {
			if credit != 0 {
				h.PushCurrentBalance(peer)
			}
			if len(updates) == 0 {
				return
			}
			if err := peer.SendPush(pushAchievementsUpdated, func(w *demonware.Writer) { w.Struct(updates) }); err != nil {
				peer.Logf("[ctr] achievements push: %v", err)
				return
			}
			peer.Logf("[ctr] achievements -> push %d achievements-updated", pushAchievementsUpdated)
		}
		result.Reply = h.StructTaskReply(task, nil)
		return result

	case TaskGetAchievementStates:
		a, err := h.Economy.Snapshot(peer.PID())
		if err != nil {
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		state := CurrentPitStopRefresh(a, peer.Now())
		var states []byte
		if AchievementRequested(payload, state) {
			states = demonware.AppendBytesField(nil, 1, state.Encode())
		}
		for _, rule := range challengeRules.Rules {
			if challenge, _, active := rule.Current(a, peer.Now()); active && AchievementRequested(payload, challenge) {
				states = demonware.AppendBytesField(states, 1, challenge.Encode())
			}
		}
		result.Reply = h.StructTaskReply(task, demonware.AppendBytesField(states, 2, nil))
		return result

	case TaskGetUserState:
		// field 1 is the engine's user-state blob. Empty JSON keeps it valid and lets
		// handleGetUserState fire CAEUserStateRefreshed, which is the refresh confirmation.
		peer.Logf("[ctr] achievements getUserState -> empty state")
		result.Reply = h.StructTaskReply(task, demonware.AppendBytesField(nil, 1, []byte("{}")))
		return result
	}
	// reportEvents and the activate/claim tasks take a request and return no body.
	result.Reply = h.StructTaskReply(task, nil)
	return result
}

// eventNames pulls the printable strings out of a reportEvents struct buffer so the race
// reward event can be identified without dumping the whole payload.
func eventNames(payload []byte) []string {
	sb := demonware.TaskStructPayload(payload)
	out := []string{}
	for i := 0; i < len(sb) && len(out) < 12; {
		wire := sb[i] & 7
		i++
		switch wire {
		case 0:
			_, next, err := demonware.ReadVarint(sb, i)
			if err != nil {
				return out
			}
			i = next
		case 2:
			ln, next, err := demonware.ReadVarint(sb, i)
			if err != nil || next+int(ln) > len(sb) {
				return out
			}
			i = next
			if s := string(sb[i : i+int(ln)]); printableName(s) {
				out = append(out, s)
			}
			i += int(ln)
		default:
			return out
		}
	}
	return out
}

func printableName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}
