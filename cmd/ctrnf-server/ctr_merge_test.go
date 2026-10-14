package main

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/ctr"
)

func mergeTestRooms(t *testing.T) (*ctr.Matchmaker, *ctr.Room, *ctr.Room) {
	m := &ctr.Matchmaker{OnlinePIDs: testPresence.OnlinePIDs, Rooms: map[uint64]*ctr.Room{}}
	a := fillTestRoom(t, m, 10000, 3)
	a.HostDoc["lobby_open"] = false
	b := fillTestRoom(t, m, 20000, 3)
	a.HostDoc["lobby_open"] = true
	return m, a, b
}
