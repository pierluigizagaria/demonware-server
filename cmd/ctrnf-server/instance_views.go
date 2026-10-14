package main

import (
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

func newDashboardViews(presenceStore *presence.PresenceStore, matchmaker *ctr.CtrMatchmaker, registry *lobby.SessionRegistry) dashboard.DashboardViews {
	return dashboard.DashboardViews{Online: presenceStore.Snapshot, Sessions: registry.Snapshot, Rooms: matchmaker.DashboardRooms}
}
