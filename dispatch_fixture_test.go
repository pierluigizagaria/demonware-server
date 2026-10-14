package main

import "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"

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
	return (&presenceHandler{replyCounter: l.replyCounter, presence: l.presence}).onGetUserNames(l, task, r)
}
func (l *lobbyConn) onUserData(task byte, r *demonware.Reader) []byte {
	return (&presenceHandler{replyCounter: l.replyCounter, presence: l.presence}).onUserData(l, task, r)
}
func (l *lobbyConn) onGetUserIDs(task byte, r *demonware.Reader) []byte {
	return (&presenceHandler{replyCounter: l.replyCounter, presence: l.presence}).onGetUserIDs(l, task, r)
}
func (l *lobbyConn) onPlatformIdentityReport(task byte, r *demonware.Reader) []byte {
	return (&platformHandler{replyCounter: l.replyCounter}).onPlatformIdentityReport(l, task, r)
}
func (l *lobbyConn) onRichPresence(task byte, r *demonware.Reader) []byte {
	return (&presenceHandler{replyCounter: l.replyCounter, presence: l.presence}).onRichPresence(l, task, r)
}
