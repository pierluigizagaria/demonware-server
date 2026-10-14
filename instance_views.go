package main

func newDashboardViews(presence *presenceStore, matchmaker *ctrMatchmaker, registry *sessionRegistry) dashboardViews {
	return dashboardViews{online: presence.snapshot, sessions: registry.snapshot, rooms: matchmaker.dashboardRooms}
}
