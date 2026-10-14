package main

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/diablo"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/nat"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

// Legacy fixtures share this explicit test owner. Production has no fallback
// ticket directory; auth, lobby and Umbrella receive the same instance owner.
var testTickets = auth.NewTicketStore()

func testAuthHandler(p auth.PublisherPlatform, s *auth.CTRSigner, dumps string) http.Handler {
	return auth.NewPlatformAuthHandler(testDiagnostics, testTelemetry, testTickets, testPresence, p, s, dumps, ctr.NewUmbrellaHandler(testDiagnostics, testTickets))
}

var testPresence = presence.NewStore(nil)

func init() { testMatchmaker.OnlinePIDs = testPresence.OnlinePIDs }

func testBuildGameStats(game string) dashboard.APIStats {
	return testTelemetry.BuildGameStats(newDashboardViews(testPresence, testMatchmaker, testRegistry), game)
}

func TestInstancePresenceAndPersistentNamesAreIsolated(t *testing.T) {
	firstDB := openTestCTR(t, filepath.Join(t.TempDir(), store.DBFile), false)
	secondDB := openTestCTR(t, filepath.Join(t.TempDir(), store.DBFile), false)
	a, b := presence.NewStore(firstDB), presence.NewStore(secondDB)
	now := time.Now()
	if a.RememberPlayerName(platform.PlayerID{PID: 17, Username: "first"}, now) != nil || b.RememberPlayerName(platform.PlayerID{PID: 17, Username: "second"}, now) != nil {
		t.Fatal("record names")
	}
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.Presence, second.Presence = a, b
	first.Player.Username, second.Player.Username = "first", "second"
	a.RegisterOnline(first.N, first.Player)
	b.RegisterOnline(second.N, second.Player)
	aw, bw := &architectureWire{}, &architectureWire{}
	first.C, second.C = aw, bw
	for _, tc := range []struct {
		l     *lobbyConn
		w     *architectureWire
		name  string
		value byte
	}{{first, aw, "first", 1}, {second, bw, "second", 2}} {
		q := demonware.NewWriter([]byte{12, 3, 10})
		q.Uint32(2)
		q.String("first")
		q.String("second")
		p := architectureReply(t, architectureCall(t, tc.l, tc.w, q.Bytes()), 10, 0)
		r := demonware.NewReader(p[26:])
		pid, _ := r.Uint64()
		name, err := r.String()
		if err != nil || pid != 17 || name != tc.name || len(r.Remaining()) != 0 {
			t.Fatal("name crossed instance boundary")
		}
		q = demonware.NewWriter([]byte{68, 3, 3})
		q.Raw(rpSetRequest("fixture", 17, "psn", 1, []byte{tc.value}))
		architectureReply(t, architectureCall(t, tc.l, tc.w, q.Bytes()), 3, 0)
	}
	aRows, _ := rpRows(t, first.onRichPresence(presence.RPGet, demonware.NewReader(rpGetRequest("fixture", 17))))
	bRows, _ := rpRows(t, second.onRichPresence(presence.RPGet, demonware.NewReader(rpGetRequest("fixture", 17))))
	if len(aRows) != 1 || len(bRows) != 1 || !bytes.Equal(aRows[0].Data, []byte{1}) || !bytes.Equal(bRows[0].Data, []byte{2}) {
		t.Fatal("presence crossed instance boundary")
	}
	a.DropOnline(first.N)
	if a.Counts().RichPresence != 0 || b.NicknameOnline(17) != "second" || b.Counts().RichPresence != 1 {
		t.Fatal("disconnect affected another instance")
	}
	q := demonware.NewWriter([]byte{12, 3, 10})
	q.Uint32(1)
	q.String("first")
	p, _ := hex.DecodeString(architectureCall(t, first, aw, q.Bytes())[0].Payload)
	r := demonware.NewReader(p[26:])
	pid, _ := r.Uint64()
	name, err := r.String()
	if err != nil || pid != 17 || name != "first" {
		t.Fatal("persistent name lost on disconnect")
	}
}

var testReplies = &lobby.ReplyCounter{}

var testStartBalance int64

var testCTR = &ctr.Services{Economy: &ctr.EconomyStore{InitialBalance: func() int64 { return testStartBalance }}, Stats: &ctr.StatsStore{}}

func testLoadWallets() { testCTR.Economy.LoadWallets(testConfig.DataDir) }

func init() {
	testCTR.Stats.(*ctr.StatsStore).FallbackDir = func() string { return testCTR.Economy.Dir }
}

func TestInstanceEconomyAndLeaderboardAreIsolated(t *testing.T) {
	a, aDB := newTestEconomy(t, false)
	b, bDB := newTestEconomy(t, false)
	aDB.InitialBalance = func() int64 { return 10000 }
	bDB.InitialBalance = func() int64 { return 20000 }
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.ctr = &ctr.Services{Economy: a, Stats: aDB}
	second.ctr = &ctr.Services{Economy: b, Stats: bDB}
	first.C = &architectureWire{}
	second.C = &architectureWire{}
	for _, l := range []*lobbyConn{first, second} {
		frames := architectureCall(t, l, l.C.(*architectureWire), marketplaceHex(t, ps4BalancesRequest))
		payload, _ := hex.DecodeString(frames[0].Payload)
		want := ctr.GetPlayerBalancesResponse(map[uint64]int64{10: l.ctr.Economy.Backend.(*store.SQLite).StartingBalance()}, 3)
		if !bytes.Equal(structBodyOf(t, payload), want) {
			t.Fatal("balance crossed instance boundary")
		}
	}
	architectureCall(t, first, first.C.(*architectureWire), marketplaceHex(t, ps4PurchaseRequest))
	aa, errA := a.Snapshot(first.PID())
	bb, errB := b.Snapshot(second.PID())
	if errA != nil || errB != nil || aa.Balances[10] >= 10000 || bb.Balances[10] != 20000 || len(bb.Items) != 0 {
		t.Fatal("purchase crossed instance boundary")
	}
	if err := aDB.Write([]ctr.StatWrite{{Stat: ctr.Stat{Board: 89, PID: first.PID(), Score: 7}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := bDB.Read(89)
	if err != nil || len(rows) != 0 {
		t.Fatal("leaderboard crossed instance boundary")
	}
}

var testMatchmaker = &ctr.Matchmaker{Rooms: map[uint64]*ctr.Room{}}

var testRegistry = lobby.NewSessionRegistry()

func TestInstanceMatchmakingAndPushesAreIsolated(t *testing.T) {
	a := &ctr.Matchmaker{OnlinePIDs: func() []uint64 { return []uint64{17, 18} }, Rooms: map[uint64]*ctr.Room{}}
	b := &ctr.Matchmaker{OnlinePIDs: func() []uint64 { return []uint64{17, 18} }, Rooms: map[uint64]*ctr.Room{}}
	prepare := func(m *ctr.Matchmaker, pid uint64) (*ctr.Search, *architectureWire) {
		s := testCTRSearchFor(m, t, pid, 0)
		l, _ := sharedPS4Lobby()
		l.N, l.Player.PID, l.matchmaker = pid, pid, m
		w := &architectureWire{}
		l.C, s.Conn = w, l
		return s, w
	}
	aa, aw := prepare(a, 17)
	ba, bw := prepare(b, 17)
	ab, _ := prepare(a, 18)
	bb, _ := prepare(b, 18)
	if aa.ID != ba.ID || ab.ID != bb.ID {
		t.Fatal("instance search ID sequences differ")
	}
	a.Enqueue(aa)
	b.Enqueue(ba)
	notices := a.Enqueue(ab)
	ctr.SendNotices(notices)
	if len(aw.writes) != 1 || len(bw.writes) != 0 || len(a.Rooms) != 1 || len(b.Rooms) != 0 {
		t.Fatal("match or push crossed instances")
	}
	architectureFrames(t, aa.Conn.(*lobbyConn), aw, 0)
	notices = b.Enqueue(bb)
	ctr.SendNotices(notices)
	if len(aw.writes) != 1 || len(bw.writes) != 1 || len(b.Rooms) != 1 {
		t.Fatal("second instance failed to form its own room")
	}
	architectureFrames(t, ba.Conn.(*lobbyConn), bw, 0)
	a.Leave(aa.Conn, 0)
	if len(a.Rooms) != 0 || len(b.Rooms) != 1 {
		t.Fatal("disconnect crossed instances")
	}
}

var testDiagnostics = &diagnostics.Diagnostics{RedactLogs: true, DumpRoot: "off", RunStamp: time.Now().Format("20060102-150405")}

var testFiles = &diablo.PublisherFiles{Diagnostics: testDiagnostics, PubDir: "pubfiles", PubConfigPath: "pubfiles.json", RiftDir: "riftdata"}

var testOptions = &lobby.Options{PublicHost: "127.0.0.1"}

var testRelay = relay.NewTable(testDiagnostics, "", "")

var testConfig = func() config.Config {
	c, err := config.Load(func(string) string { return "" }, nil, false)
	if err != nil {
		panic(err)
	}
	return c
}()

var testNAT = nat.NewServer(testDiagnostics, "", "", &atomic.Uint64{})

var testTelemetry = dashboard.NewTelemetry(testConfig, testNAT.Introductions)

func testApplyConfig(c config.Config) {
	testConfig = c
	testDiagnostics.RedactLogs, testDiagnostics.Verbose, testDiagnostics.DumpRoot = c.RedactLogs, c.Verbose, c.Dumps
	testFiles.PubDir, testFiles.PubConfigPath, testFiles.RiftDir = c.PubfilesDir, c.PubfilesConfig, c.RiftData
	testOptions.MigrateEnabled, testOptions.PublicHost = c.Migrate, c.PublicIP
	testRelay.BindAddr, testRelay.PIDs = c.BindAddr, c.RelayPIDs
}

func TestComposedInstancesKeepEqualIdentitiesIndependent(t *testing.T) {
	var instances []*serverInstance
	var lobbies []*lobbyConn
	var wires []*architectureWire
	for n, text := range []string{"first", "second"} {
		cfg, err := config.Load(lookupFrom(map[string]string{"CTR_NEWS": text, "CTR_NEWS_IT": text}), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DataDir = t.TempDir()
		cfg.StartBalance = int64((n + 1) * 10000)
		database := openTestCTR(t, filepath.Join(cfg.DataDir, store.DBFile), false)
		instance := newInstance(cfg, database)
		// Mutating the caller's copy cannot rewrite the instance configuration.
		cfg.News.Languages["it"] = config.NewsMessage{}
		w := &architectureWire{}
		l := &lobbyConn{Session: instance.lobby.NewConn(w, ""), lobbyDependencies: lobbyDependencies{files: testFiles, relay: instance.relay, matchmaker: instance.matchmaker, registry: instance.registry, ctr: instance.ctr}}
		fixture, _ := sharedPS4Lobby()
		l.Keys, l.Player = fixture.Keys, fixture.Player
		l.Player.Username = text
		now := time.Now()
		ticket := psnTicket(now)
		instance.tickets.RememberTicket(ticket, *l.Player, now, ps4.TicketWindow)
		if err := instance.presence.RememberPlayerName(*l.Player, now); err != nil {
			t.Fatal(err)
		}
		instance.presence.RegisterOnline(l.N, l.Player)
		frames := architectureCall(t, l, w, newsFetch("ctr", "it", [2]uint32{1, 1}))
		payload, _ := hex.DecodeString(frames[0].Payload)
		r := newsReply(t, payload, 5, 0, 1)
		newsLocation(t, r, 1, 1)
		newsMessage(t, r, "it", text)
		newsEnd(t, r)
		instances = append(instances, instance)
		lobbies = append(lobbies, l)
		wires = append(wires, w)
	}
	if lobbies[0].N != lobbies[1].N || lobbies[0].PID() != lobbies[1].PID() {
		t.Fatal("fixture must reuse numeric identities")
	}
	for n, instance := range instances {
		q := demonware.NewWriter([]byte{12, 3, 10})
		q.Uint32(2)
		q.String("first")
		q.String("second")
		p := architectureReply(t, architectureCall(t, lobbies[n], wires[n], q.Bytes()), 10, 0)
		r := demonware.NewReader(p[26:])
		pid, _ := r.Uint64()
		name, err := r.String()
		if err != nil || pid != lobbies[n].PID() || name != lobbies[n].Player.Username || len(r.Remaining()) != 0 {
			t.Fatal("composed name stores crossed")
		}
		a, err := instance.ctr.Economy.Snapshot(pid)
		if err != nil || a.Balances[10] != int64((n+1)*10000) {
			t.Fatal("composed balances crossed")
		}
	}
}
