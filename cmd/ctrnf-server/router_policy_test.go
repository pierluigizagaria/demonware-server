package main

import (
	"fmt"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/diablo"
)

func TestUnreadableTaskPolicyInBothTables(t *testing.T) {
	for _, table := range []string{"ctr", "diablo"} {
		for _, service := range []byte{4, 80, 104, 255} {
			t.Run(fmt.Sprintf("%s/%d", table, service), func(t *testing.T) {
				l, wire, _ := architectureBaseline(t)
				l.fixtureTable()
				if table == "diablo" {
					l.Table = diablo.NewServiceTable(l.ReplyCounter, l.registry, l.Presence, l.relay, l.files)
				}
				var code uint32
				if service == 80 || service == 104 {
					code = 1
				}
				architectureReply(t, architectureCall(t, l, wire, []byte{service, 1, 6}), 6, code)
			})
		}
	}
}
