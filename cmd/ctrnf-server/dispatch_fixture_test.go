package main

import (
	ctrpkg "github.com/NextendoNetwork/nextendo-demonware/internal/ctr"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	diablopkg "github.com/NextendoNetwork/nextendo-demonware/internal/diablo"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
	strings "strings"
)

// Direct-handler compatibility fixtures use the same explicit dependencies.
func (l *lobbyConn) onAchievements(task byte, payload []byte) (result lobbypkg.TaskResult) {
	return (&ctrpkg.EconomyHandler{ReplyCounter: l.ReplyCounter, Economy: l.ctr.Economy}).OnAchievements(l, task, payload)
}

func (l *lobbyConn) onMarketplace(task byte, payload []byte) (result lobbypkg.TaskResult) {
	return (&ctrpkg.EconomyHandler{ReplyCounter: l.ReplyCounter, Economy: l.ctr.Economy}).OnMarketplace(l, task, payload)
}

func (l *lobbyConn) onMarketingComms(task byte, r *demonwarepkg.Reader) []byte {
	return (&ctrpkg.NewsHandler{ReplyCounter: l.ReplyCounter, News: l.ctr.News}).OnMarketingComms(l, task, r)
}

func (l *lobbyConn) pushCurrentBalance() {
	(&ctrpkg.EconomyHandler{ReplyCounter: l.ReplyCounter, Economy: l.ctr.Economy}).PushCurrentBalance(l)
}

func (l *lobbyConn) onCTRStats(task byte, r *demonwarepkg.Reader) []byte {
	return (&ctrpkg.StatsHandler{ReplyCounter: l.ReplyCounter, Stats: l.ctr.Stats, Presence: l.Presence}).OnCTRStats(l, task, r)
}

func (l *lobbyConn) onGetUserNames(task byte, r *demonwarepkg.Reader) []byte {
	return (&presencepkg.PresenceHandler{PresenceReplies: l.ReplyCounter, Presence: l.Presence}).OnGetUserNames(l, task, r)
}

func (l *lobbyConn) onUserData(task byte, r *demonwarepkg.Reader) []byte {
	return (&presencepkg.PresenceHandler{PresenceReplies: l.ReplyCounter, Presence: l.Presence}).OnUserData(l, task, r)
}

func (l *lobbyConn) onGetUserIDs(task byte, r *demonwarepkg.Reader) []byte {
	return (&presencepkg.PresenceHandler{PresenceReplies: l.ReplyCounter, Presence: l.Presence}).OnGetUserIDs(l, task, r)
}

func (l *lobbyConn) onPlatformIdentityReport(task byte, r *demonwarepkg.Reader) []byte {
	return (&ctrpkg.PlatformHandler{ReplyCounter: l.ReplyCounter}).OnPlatformIdentityReport(l, task, r)
}

func (l *lobbyConn) onRichPresence(task byte, r *demonwarepkg.Reader) []byte {
	return (&presencepkg.PresenceHandler{PresenceReplies: l.ReplyCounter, Presence: l.Presence}).OnRichPresence(l, task, r)
}

func (l *lobbyConn) owns(s *lobbypkg.MmSession) bool {
	return (&lobbypkg.ContextHandler{ReadInfo: diablopkg.ReadInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}).Owns(l, s)
}

func (l *lobbyConn) onFriendSessions(task byte, r *demonwarepkg.Reader, context string) []byte {
	return (&lobbypkg.ContextHandler{ReadInfo: diablopkg.ReadInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}).OnFriendSessions(l, task, r, context)
}

func (l *lobbyConn) onMatchMaking(task byte, r *demonwarepkg.Reader) []byte {
	return (&diablopkg.DiabloMatchHandler{ContextHandler: &lobbypkg.ContextHandler{ReadInfo: diablopkg.ReadInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}}).OnMatchMaking(l, task, r)
}

func (l *lobbyConn) onMatchMakingContext(task byte, r *demonwarepkg.Reader, context string) []byte {
	readInfo := diablopkg.ReadInfo
	if strings.HasPrefix(context, "ctr:") {
		readInfo = ctrpkg.ReadCTRInfo
	}
	return (&lobbypkg.ContextHandler{ReadInfo: readInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}).OnMatchMakingContext(l, task, r, context)
}

func (l *lobbyConn) PublisherFile(name string) ([]byte, string) {
	files := *l.files
	files.PubDir = l.pubDir
	return files.PublisherFile(name, l.Now())
}

// Legacy fixtures replace dependencies between calls. Keep that test-only
// wiring outside the production router, which receives one immutable table.
func (l *lobbyConn) onTask(payload []byte) {
	if l.Table != nil {
		l.ProcessTask(payload)
		return
	}
	table, async := ctrpkg.NewCTRServiceTable(l.ReplyCounter, l.ctr, l.Presence, l.registry, l.matchmaker, l.pubDir, l.relay)
	async.SetPlayerInfo(l.N, l.lobbyDoc)
	l.Table = table
	defer func() { l.Table = nil; l.lobbyDoc = async.PlayerInfo(l.N) }()
	l.ProcessTask(payload)
}

func (l *lobbyConn) fixtureTable() {
	l.Table, _ = ctrpkg.NewCTRServiceTable(l.ReplyCounter, l.ctr, l.Presence, l.registry, l.matchmaker, l.pubDir, l.relay)
}
