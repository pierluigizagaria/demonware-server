package main

import (
	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

// Achievements use struct task replies; ordinary result-count replies fail decoding.
const svcAchievements = 125

// Push 75 carries named achievement states, not a generic acknowledgement.
const pushAchievementsUpdated = 75

const (
	taskReportEvents         = 1
	taskReportUserEvents     = 2
	taskGetAchievementStates = 3
	taskGetUserState         = 9
)

// onAchievements answers every achievements-engine task with a well formed struct.
func (l *lobbyConn) onAchievements(task byte, payload []byte) (result taskResult) {
	switch task {
	case taskReportEvents, taskReportUserEvents:
		var credit int64
		var updates []byte
		if task == taskReportEvents {
			events, err := parseRewardEvents(payload)
			if err != nil {
				l.logf("CTR reward event rejected: %v", err)
				result.Reply = l.taskReply(task, errUnhandled, nil)
				return result
			}
			for _, e := range events {
				l.logf("CTR reward event=%s timestamp=%d values=%v", e.Name, e.Timestamp, e.Values)
			}
			credit, err = l.ctr.economy.awardRaces(l.pid(), events, l.now())
			if err != nil {
				l.logf("CTR reward persistence failed: %v", err)
				result.Reply = l.taskReply(task, errUnhandled, nil)
				return result
			}
			updates, err = l.ctr.economy.reportPitStopRefresh(l.pid(), events, l.now())
			if err != nil {
				l.logf("CTR refresh persistence failed: %v", err)
				result.Reply = l.taskReply(task, errUnhandled, nil)
				return result
			}
			challengeCredit, challengeUpdates, err := l.ctr.economy.awardChallenges(l.pid(), events, l.now())
			if err != nil {
				l.logf("CTR challenge persistence failed: %v", err)
				result.Reply = l.taskReply(task, errUnhandled, nil)
				return result
			}
			credit += challengeCredit
			updates = append(updates, challengeUpdates...)
			if challengeCredit != 0 {
				l.logf("CTR challenge reward=%d persisted", challengeCredit)
			}

			if credit != 0 {
				l.logf("CTR reward credit=%d currency=10 persisted", credit)
			}
		}
		result.AfterReply = func(_ error) {
			if credit != 0 {
				l.pushCurrentBalance()
			}
			if len(updates) == 0 {
				return
			}
			if err := l.sendPush(pushAchievementsUpdated, func(w *demonware.Writer) { w.Struct(updates) }); err != nil {
				l.logf("CTR achievements push: %v", err)
				return
			}
			l.logf("CTR achievements -> push %d achievements-updated", pushAchievementsUpdated)
		}
		result.Reply = l.structTaskReply(task, nil)
		return result

	case taskGetAchievementStates:
		a, err := l.ctr.economy.snapshot(l.pid())
		if err != nil {
			result.Reply = l.taskReply(task, errUnhandled, nil)
			return result
		}
		state := currentPitStopRefresh(a, l.now())
		var states []byte
		if achievementRequested(payload, state) {
			states = demonware.AppendBytesField(nil, 1, state.encode())
		}
		for _, rule := range challengeRules.Rules {
			if challenge, _, active := rule.current(a, l.now()); active && achievementRequested(payload, challenge) {
				states = demonware.AppendBytesField(states, 1, challenge.encode())
			}
		}
		result.Reply = l.structTaskReply(task, demonware.AppendBytesField(states, 2, nil))
		return result

	case taskGetUserState:
		// field 1 is the engine's user-state blob. Empty JSON keeps it valid and lets
		// handleGetUserState fire CAEUserStateRefreshed, which is the refresh confirmation.
		l.logf("CTR achievements getUserState -> empty state")
		result.Reply = l.structTaskReply(task, demonware.AppendBytesField(nil, 1, []byte("{}")))
		return result
	}
	// reportEvents and the activate/claim tasks take a request and return no body.
	result.Reply = l.structTaskReply(task, nil)
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
