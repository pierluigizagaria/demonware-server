package main

import (
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
)

func TestCTRNewsConfigAppliedPerInstance(t *testing.T) {
	request := newsFetch("ctr", "en", [2]uint32{1, 1})
	for _, tc := range []struct{ mode, text string }{{"0", "Normal news"}, {"1", "Other news"}, {"0", "Other news"}} {
		c, err := config.Load(lookupFrom(map[string]string{"CTR_NEWS": tc.text, "CTR_UNLOCK_ALL": tc.mode}), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		l, wire := sharedPS4Lobby()
		l.ctr = &ctr.Services{News: c.News}
		r := newsReply(t, sharedTask(t, l, wire, request), 5, 0, 1)
		newsLocation(t, r, 1, 1)
		newsMessage(t, r, "en", tc.text)
		newsEnd(t, r)
	}
}
