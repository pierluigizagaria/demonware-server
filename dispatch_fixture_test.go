package main

import (
	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	"strings"
)

// Direct-handler compatibility fixtures use the same explicit dependencies.
func (l *lobbyConn) onAchievements(task byte, payload []byte) (result taskResult) {
	return (&economyHandler{replyCounter: l.replyCounter, economy: l.ctr.economy}).onAchievements(l, task, payload)
}
func (l *lobbyConn) onMarketplace(task byte, payload []byte) (result taskResult) {
	return (&economyHandler{replyCounter: l.replyCounter, economy: l.ctr.economy}).onMarketplace(l, task, payload)
}
func (l *lobbyConn) onMarketingComms(task byte, r *demonware.Reader) []byte {
	return (&newsHandler{replyCounter: l.replyCounter, news: l.ctr.news}).onMarketingComms(l, task, r)
}
func (l *lobbyConn) pushCurrentBalance() {
	(&economyHandler{replyCounter: l.replyCounter, economy: l.ctr.economy}).pushCurrentBalance(l)
}
func (l *lobbyConn) onCTRStats(task byte, r *demonware.Reader) []byte {
	return (&statsHandler{replyCounter: l.replyCounter, stats: l.ctr.stats, presence: l.presence}).onCTRStats(l, task, r)
}
func (l *lobbyConn) onGetUserNames(task byte, r *demonware.Reader) []byte {
	return (&presenceHandler{presenceReplies: l.replyCounter, presence: l.presence}).onGetUserNames(l, task, r)
}
func (l *lobbyConn) onUserData(task byte, r *demonware.Reader) []byte {
	return (&presenceHandler{presenceReplies: l.replyCounter, presence: l.presence}).onUserData(l, task, r)
}
func (l *lobbyConn) onGetUserIDs(task byte, r *demonware.Reader) []byte {
	return (&presenceHandler{presenceReplies: l.replyCounter, presence: l.presence}).onGetUserIDs(l, task, r)
}
func (l *lobbyConn) onPlatformIdentityReport(task byte, r *demonware.Reader) []byte {
	return (&platformHandler{replyCounter: l.replyCounter}).onPlatformIdentityReport(l, task, r)
}
func (l *lobbyConn) onRichPresence(task byte, r *demonware.Reader) []byte {
	return (&presenceHandler{presenceReplies: l.replyCounter, presence: l.presence}).onRichPresence(l, task, r)
}

func (l *lobbyConn) owns(s *mmSession) bool {
	return (&contextHandler{readInfo: readInfo, replyCounter: l.replyCounter, registry: l.registry, presence: l.presence, relay: l.relay}).owns(l, s)
}
func (l *lobbyConn) onFriendSessions(task byte, r *demonware.Reader, context string) []byte {
	return (&contextHandler{readInfo: readInfo, replyCounter: l.replyCounter, registry: l.registry, presence: l.presence, relay: l.relay}).onFriendSessions(l, task, r, context)
}
func (l *lobbyConn) onMatchMaking(task byte, r *demonware.Reader) []byte {
	return (&diabloMatchHandler{&contextHandler{readInfo: readInfo, replyCounter: l.replyCounter, registry: l.registry, presence: l.presence, relay: l.relay}}).onMatchMaking(l, task, r)
}
func (l *lobbyConn) onMatchMakingContext(task byte, r *demonware.Reader, context string) []byte {
	readInfo := readInfo
	if strings.HasPrefix(context, "ctr:") {
		readInfo = readCTRInfo
	}
	return (&contextHandler{readInfo: readInfo, replyCounter: l.replyCounter, registry: l.registry, presence: l.presence, relay: l.relay}).onMatchMakingContext(l, task, r, context)
}
func (l *lobbyConn) publisherFile(name string) ([]byte, string) {
	files := *l.files
	files.pubDir = l.pubDir
	return files.publisherFile(name, l.Now())
}

// Legacy fixtures replace dependencies between calls. Keep that test-only
// wiring outside the production router, which receives one immutable table.
func (l *lobbyConn) onTask(payload []byte) {
	if l.table != nil {
		l.processTask(payload)
		return
	}
	table, async := newCTRServiceTable(l.replyCounter, l.ctr, l.presence, l.registry, l.matchmaker, l.pubDir, l.relay)
	async.setPlayerInfo(l.n, l.lobbyDoc)
	l.table = table
	defer func() { l.table = nil; l.lobbyDoc = async.playerInfo(l.n) }()
	l.processTask(payload)
}
func (l *lobbyConn) fixtureTable() {
	l.table, _ = newCTRServiceTable(l.replyCounter, l.ctr, l.presence, l.registry, l.matchmaker, l.pubDir, l.relay)
}
