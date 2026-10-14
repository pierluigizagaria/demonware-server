package ctr

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

// A match fixture owns its connection allocator as well as the engine under test.
type matchFixture struct {
	Matchmaker
	nextConn uint64
}

type testPeer struct {
	N        uint64
	Player   *platform.PlayerID
	Presence *presence.Store
	services *Services
	replies  *lobby.ReplyCounter
	lobbyDoc string
}

func newUnitPeer(t *testing.T, p *testPeer) *testPeer {
	t.Helper()
	p.replies = &lobby.ReplyCounter{}
	if p.Presence == nil {
		p.Presence = presence.NewStore(nil)
	}
	if p.services == nil {
		p.services = &Services{Economy: &EconomyStore{Dir: t.TempDir()}, Stats: &StatsStore{Dir: t.TempDir()}}
	}
	return p
}
func (p *testPeer) ConnID() uint64 { return p.N }
func (p *testPeer) PID() uint64 {
	if p.Player == nil {
		return 0
	}
	return p.Player.PID
}
func (p *testPeer) Identity() *platform.PlayerID {
	if p.Player == nil {
		return nil
	}
	copy := *p.Player
	return &copy
}
func (p *testPeer) Title() uint32 {
	if p.Player == nil {
		return 0
	}
	return p.Player.Title
}
func (*testPeer) Logf(string, ...any)                            {}
func (*testPeer) SendPush(uint32, func(*demonware.Writer)) error { return nil }
func (*testPeer) Now() time.Time                                 { return time.Now() }
func (*testPeer) Random(b []byte) (int, error)                   { return rand.Read(b) }
func (p *testPeer) onMarketplace(task byte, payload []byte) lobby.TaskResult {
	return (&EconomyHandler{ReplyCounter: p.replies, Economy: p.services.Economy}).OnMarketplace(p, task, payload)
}
func (p *testPeer) onAchievements(task byte, payload []byte) lobby.TaskResult {
	return (&EconomyHandler{ReplyCounter: p.replies, Economy: p.services.Economy}).OnAchievements(p, task, payload)
}
func (p *testPeer) onCTRStats(task byte, r *demonware.Reader) []byte {
	return (&StatsHandler{ReplyCounter: p.replies, Stats: p.services.Stats, Presence: p.Presence}).OnStats(p, task, r)
}

func testCTRSearchFor(m *matchFixture, t *testing.T, pid uint64, playlist int) *Search {
	t.Helper()
	info := PlayerInfo{}
	info.Listen.Address = base64.StdEncoding.EncodeToString(make([]byte, 67))
	info.Listen.ID = base64.StdEncoding.EncodeToString(make([]byte, 8))
	info.Listen.Key = base64.StdEncoding.EncodeToString(make([]byte, 16))
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	m.nextConn++
	peer := &testPeer{N: m.nextConn, Player: &platform.PlayerID{PID: pid}, lobbyDoc: string(raw)}
	search, err := m.ParseSearch(peer, fmt.Sprintf(`{"members":[%d],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":%d,"matchmaking_version":200}}}`, pid, playlist), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return search
}
