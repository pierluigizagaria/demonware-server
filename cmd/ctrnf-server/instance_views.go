package main

import (
	ctrpkg "github.com/pierluigizagaria/demonware-server/internal/ctr"
	dashboardpkg "github.com/pierluigizagaria/demonware-server/internal/dashboard"
	lobbypkg "github.com/pierluigizagaria/demonware-server/internal/lobby"
	presencepkg "github.com/pierluigizagaria/demonware-server/internal/presence"
)

func newDashboardViews(presence *presencepkg.PresenceStore, matchmaker *ctrpkg.CtrMatchmaker, registry *lobbypkg.SessionRegistry) dashboardpkg.DashboardViews {
	return dashboardpkg.DashboardViews{Online: presence.Snapshot, Sessions: registry.Snapshot, Rooms: matchmaker.DashboardRooms}
}
