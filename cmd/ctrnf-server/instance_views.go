package main

import (
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

func newDashboardViews(presenceStore *presence.Store, matchmaker *ctr.Matchmaker, registry *lobby.SessionRegistry) dashboard.Views {
	return dashboard.Views{Online: presenceStore.Snapshot, Sessions: registry.Snapshot, Rooms: matchmaker.DashboardRooms}
}
