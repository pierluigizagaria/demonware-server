package main

import (
	ctrpkg "github.com/NextendoNetwork/nextendo-demonware/internal/ctr"
	dashboardpkg "github.com/NextendoNetwork/nextendo-demonware/internal/dashboard"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
)

func newDashboardViews(presence *presencepkg.PresenceStore, matchmaker *ctrpkg.CtrMatchmaker, registry *lobbypkg.SessionRegistry) dashboardpkg.DashboardViews {
	return dashboardpkg.DashboardViews{Online: presence.Snapshot, Sessions: registry.Snapshot, Rooms: matchmaker.DashboardRooms}
}
