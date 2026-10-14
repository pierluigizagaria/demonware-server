package main

import (
	"strings"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/diablo"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

// Direct-handler compatibility fixtures use the same explicit dependencies.
func (l *lobbyConn) onAchievements(task byte, payload []byte) (result lobby.TaskResult) {
	return (&ctr.EconomyHandler{ReplyCounter: l.ReplyCounter, Economy: l.ctr.Economy}).OnAchievements(l, task, payload)
}

func (l *lobbyConn) onMarketplace(task byte, payload []byte) (result lobby.TaskResult) {
	return (&ctr.EconomyHandler{ReplyCounter: l.ReplyCounter, Economy: l.ctr.Economy}).OnMarketplace(l, task, payload)
}

func (l *lobbyConn) onMarketingComms(task byte, r *demonware.Reader) []byte {
	return (&ctr.NewsHandler{ReplyCounter: l.ReplyCounter, News: l.ctr.News}).OnMarketingComms(l, task, r)
}

func (l *lobbyConn) pushCurrentBalance() {
	(&ctr.EconomyHandler{ReplyCounter: l.ReplyCounter, Economy: l.ctr.Economy}).PushCurrentBalance(l)
}

func (l *lobbyConn) onCTRStats(task byte, r *demonware.Reader) []byte {
	return (&ctr.StatsHandler{ReplyCounter: l.ReplyCounter, Stats: l.ctr.Stats, Presence: l.Presence}).OnStats(l, task, r)
}

func (l *lobbyConn) onGetUserNames(task byte, r *demonware.Reader) []byte {
	return (&presence.Handler{Replies: l.ReplyCounter, Presence: l.Presence}).OnGetUserNames(l, task, r)
}

func (l *lobbyConn) onUserData(task byte, r *demonware.Reader) []byte {
	return (&presence.Handler{Replies: l.ReplyCounter, Presence: l.Presence}).OnUserData(l, task, r)
}

func (l *lobbyConn) onGetUserIDs(task byte, r *demonware.Reader) []byte {
	return (&presence.Handler{Replies: l.ReplyCounter, Presence: l.Presence}).OnGetUserIDs(l, task, r)
}

func (l *lobbyConn) onPlatformIdentityReport(task byte, r *demonware.Reader) []byte {
	return (&ctr.PlatformHandler{ReplyCounter: l.ReplyCounter}).OnPlatformIdentityReport(l, task, r)
}

func (l *lobbyConn) onRichPresence(task byte, r *demonware.Reader) []byte {
	return (&presence.Handler{Replies: l.ReplyCounter, Presence: l.Presence}).OnRichPresence(l, task, r)
}

func (l *lobbyConn) owns(s *lobby.MatchSession) bool {
	return (&lobby.ContextHandler{ReadInfo: diablo.ReadInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}).Owns(l, s)
}

func (l *lobbyConn) onFriendSessions(task byte, r *demonware.Reader, context string) []byte {
	return (&lobby.ContextHandler{ReadInfo: diablo.ReadInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}).OnFriendSessions(l, task, r, context)
}

func (l *lobbyConn) onMatchMaking(task byte, r *demonware.Reader) []byte {
	return (&diablo.MatchHandler{ContextHandler: &lobby.ContextHandler{ReadInfo: diablo.ReadInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}}).OnMatchMaking(l, task, r)
}

func (l *lobbyConn) onMatchMakingContext(task byte, r *demonware.Reader, context string) []byte {
	readInfo := diablo.ReadInfo
	if strings.HasPrefix(context, "ctr:") {
		readInfo = ctr.ReadInfo
	}
	return (&lobby.ContextHandler{ReadInfo: readInfo, ReplyCounter: l.ReplyCounter, Registry: l.registry, Presence: l.Presence, Relay: l.relay}).OnMatchMakingContext(l, task, r, context)
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
	table, async := ctr.NewServiceTable(l.ReplyCounter, l.ctr, l.Presence, l.registry, l.matchmaker, l.pubDir, l.relay)
	async.SetPlayerInfo(l.N, l.lobbyDoc)
	l.Table = table
	defer func() { l.Table = nil; l.lobbyDoc = async.PlayerInfo(l.N) }()
	l.ProcessTask(payload)
}

func (l *lobbyConn) fixtureTable() {
	l.Table, _ = ctr.NewServiceTable(l.ReplyCounter, l.ctr, l.Presence, l.registry, l.matchmaker, l.pubDir, l.relay)
}
