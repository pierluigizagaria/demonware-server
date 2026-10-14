package main

import (
	bytes "bytes"
	hex "encoding/hex"
	authpkg "github.com/NextendoNetwork/nextendo-demonware/internal/auth"
	configpkg "github.com/NextendoNetwork/nextendo-demonware/internal/config"
	ctrpkg "github.com/NextendoNetwork/nextendo-demonware/internal/ctr"
	dashboardpkg "github.com/NextendoNetwork/nextendo-demonware/internal/dashboard"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	diablopkg "github.com/NextendoNetwork/nextendo-demonware/internal/diablo"
	diagnosticspkg "github.com/NextendoNetwork/nextendo-demonware/internal/diagnostics"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	natpkg "github.com/NextendoNetwork/nextendo-demonware/internal/nat"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	presencepkg "github.com/NextendoNetwork/nextendo-demonware/internal/presence"
	relaypkg "github.com/NextendoNetwork/nextendo-demonware/internal/relay"
	storepkg "github.com/NextendoNetwork/nextendo-demonware/internal/store"
	http "net/http"
	filepath "path/filepath"
	testing "testing"
	time "time"
)

// Legacy fixtures share this explicit test owner. Production has no fallback
// ticket directory; auth, lobby and Umbrella receive the same instance owner.
var testTickets = authpkg.NewTicketStore()

func testAuthHandler(p authpkg.PublisherPlatform, s *authpkg.CtrAuthSigner, dumps string) http.Handler {
	return authpkg.NewPlatformAuthHandler(testDiagnostics, testTelemetry, testTickets, testPresence, p, s, dumps, ctrpkg.NewUmbrellaHandler(testDiagnostics, testTickets))
}

func testUmbrellaHandler(w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
	ctrpkg.HandleCTRUmbrella(testDiagnostics, testTickets, w, r, body, n, dumps)
}

func TestInstanceTicketDirectoriesAreIsolated(t *testing.T) {
	a, b := authpkg.NewTicketStore(), authpkg.NewTicketStore()
	now := time.Now()
	ticket := psnTicket(now)
	a.RememberTicket(ticket, platformpkg.PlayerID{PID: 17, Username: "first", Title: 5760}, now, ps4pkg.TicketWindow)
	if _, ok := b.IssuedTicketPlayer(ticket, now, ps4pkg.TicketWindow); ok {
		t.Fatal("another instance admitted a foreign ticket")
	}
	b.RememberTicket(ticket, platformpkg.PlayerID{PID: 17, Username: "second", Title: 5760}, now, ps4pkg.TicketWindow)
	first, okA := a.IssuedIdentity(ticket)
	second, okB := b.IssuedIdentity(ticket)
	if !okA || !okB || first.Username != "first" || second.Username != "second" {
		t.Fatal("same numeric identity crossed instance directories")
	}
}

var testPresence = presencepkg.NewPresenceStore(nil)

func init() { testMatchmaker.OnlinePIDs = testPresence.OnlinePIDs }

func testBuildStats() dashboardpkg.ApiStats {
	return testTelemetry.BuildStats(newDashboardViews(testPresence, testMatchmaker, testRegistry))
}

func testBuildGameStats(game string) dashboardpkg.ApiStats {
	return testTelemetry.BuildGameStats(newDashboardViews(testPresence, testMatchmaker, testRegistry), game)
}

func TestInstancePresenceAndPersistentNamesAreIsolated(t *testing.T) {
	firstDB := openTestCTR(t, filepath.Join(t.TempDir(), storepkg.CtrDB), false)
	secondDB := openTestCTR(t, filepath.Join(t.TempDir(), storepkg.CtrDB), false)
	a, b := presencepkg.NewPresenceStore(firstDB), presencepkg.NewPresenceStore(secondDB)
	now := time.Now()
	if a.RememberPlayerName(platformpkg.PlayerID{PID: 17, Username: "first"}, now) != nil || b.RememberPlayerName(platformpkg.PlayerID{PID: 17, Username: "second"}, now) != nil {
		t.Fatal("record names")
	}
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.Presence, second.Presence = a, b
	first.Player.Username, second.Player.Username = "first", "second"
	a.Online[first.N], b.Online[second.N] = first.Player, second.Player
	aw, bw := &architectureWire{}, &architectureWire{}
	first.C, second.C = aw, bw
	for _, tc := range []struct {
		l     *lobbyConn
		w     *architectureWire
		name  string
		value byte
	}{{first, aw, "first", 1}, {second, bw, "second", 2}} {
		q := demonwarepkg.NewWriter([]byte{12, 3, 10})
		q.Uint32(2)
		q.String("first")
		q.String("second")
		p := architectureReply(t, architectureCall(t, tc.l, tc.w, q.Bytes()), 10, 0)
		r := demonwarepkg.NewReader(p[26:])
		pid, _ := r.Uint64()
		name, err := r.String()
		if err != nil || pid != 17 || name != tc.name || len(r.Remaining()) != 0 {
			t.Fatal("name crossed instance boundary")
		}
		q = demonwarepkg.NewWriter([]byte{68, 3, 3})
		q.Raw(rpSetRequest("fixture", 17, "psn", 1, []byte{tc.value}))
		architectureReply(t, architectureCall(t, tc.l, tc.w, q.Bytes()), 3, 0)
	}
	if !bytes.Equal(a.Rich[17].Data, []byte{1}) || !bytes.Equal(b.Rich[17].Data, []byte{2}) {
		t.Fatal("presence crossed instance boundary")
	}
	a.DropOnline(first.N)
	if len(a.Rich) != 0 || b.NicknameOnline(17) != "second" || len(b.Rich) != 1 {
		t.Fatal("disconnect affected another instance")
	}
	q := demonwarepkg.NewWriter([]byte{12, 3, 10})
	q.Uint32(1)
	q.String("first")
	p, _ := hex.DecodeString(architectureCall(t, first, aw, q.Bytes())[0].Payload)
	r := demonwarepkg.NewReader(p[26:])
	pid, _ := r.Uint64()
	name, err := r.String()
	if err != nil || pid != 17 || name != "first" {
		t.Fatal("persistent name lost on disconnect")
	}
}

var testReplies = &lobbypkg.ReplyCounter{}

func TestInstanceReplyCountersAreIsolated(t *testing.T) {
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.ReplyCounter, second.ReplyCounter = &lobbypkg.ReplyCounter{}, &lobbypkg.ReplyCounter{}
	for _, l := range []*lobbyConn{first, second, first} {
		w := &architectureWire{}
		l.C = w
		frames := architectureCall(t, l, w, []byte{255, 3, 1})
		payload, err := hex.DecodeString(frames[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := demonwarepkg.NewReader(payload).Uint64()
		if err != nil || transaction != l.Transactions.Load() {
			t.Fatal("unexpected transaction counter")
		}
	}
	if first.Transactions.Load() != 2 || second.Transactions.Load() != 1 {
		t.Fatal("transaction counters crossed instance boundary")
	}
}

var testStartBalance int64

var testCTR = &ctrpkg.CtrServices{Economy: &ctrpkg.EconomyStore{InitialBalance: func() int64 { return testStartBalance }}, Stats: &ctrpkg.CtrStatsStore{}}

func testLoadWallets() { testCTR.Economy.LoadWallets(testConfig.DataDir) }

func init() {
	testCTR.Stats.(*ctrpkg.CtrStatsStore).FallbackDir = func() string { return testCTR.Economy.Dir }
}

func TestInstanceEconomyAndLeaderboardAreIsolated(t *testing.T) {
	a, aDB := newTestEconomy(t, false)
	b, bDB := newTestEconomy(t, false)
	aDB.InitialBalance = func() int64 { return 10000 }
	bDB.InitialBalance = func() int64 { return 20000 }
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.ctr = &ctrpkg.CtrServices{Economy: a, Stats: aDB}
	second.ctr = &ctrpkg.CtrServices{Economy: b, Stats: bDB}
	first.C = &architectureWire{}
	second.C = &architectureWire{}
	for _, l := range []*lobbyConn{first, second} {
		frames := architectureCall(t, l, l.C.(*architectureWire), marketplaceHex(t, ps4BalancesRequest))
		payload, _ := hex.DecodeString(frames[0].Payload)
		want := ctrpkg.GetPlayerBalancesResponse(map[uint64]int64{10: l.ctr.Economy.Backend.(*storepkg.CtrSQLite).StartingBalance()}, 3)
		if !bytes.Equal(structBodyOf(t, payload), want) {
			t.Fatal("balance crossed instance boundary")
		}
	}
	architectureCall(t, first, first.C.(*architectureWire), marketplaceHex(t, ps4PurchaseRequest))
	aa, errA := a.Snapshot(first.Pid())
	bb, errB := b.Snapshot(second.Pid())
	if errA != nil || errB != nil || aa.Balances[10] >= 10000 || bb.Balances[10] != 20000 || len(bb.Items) != 0 {
		t.Fatal("purchase crossed instance boundary")
	}
	if err := aDB.Write([]ctrpkg.CtrStatWrite{{CtrStat: ctrpkg.CtrStat{Board: 89, PID: first.Pid(), Score: 7}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := bDB.Read(89)
	if err != nil || len(rows) != 0 {
		t.Fatal("leaderboard crossed instance boundary")
	}
}

var testMatchmaker = &ctrpkg.CtrMatchmaker{Rooms: map[uint64]*ctrpkg.CtrRoom{}}

var testRegistry = lobbypkg.NewSessionRegistry()

func TestInstanceMatchmakingAndPushesAreIsolated(t *testing.T) {
	a := &ctrpkg.CtrMatchmaker{OnlinePIDs: func() []uint64 { return []uint64{17, 18} }, Rooms: map[uint64]*ctrpkg.CtrRoom{}}
	b := &ctrpkg.CtrMatchmaker{OnlinePIDs: func() []uint64 { return []uint64{17, 18} }, Rooms: map[uint64]*ctrpkg.CtrRoom{}}
	prepare := func(m *ctrpkg.CtrMatchmaker, pid uint64) (*ctrpkg.CtrSearch, *architectureWire) {
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
	if aa.Id != ba.Id || ab.Id != bb.Id {
		t.Fatal("instance search ID sequences differ")
	}
	a.Enqueue(aa)
	b.Enqueue(ba)
	notices := a.Enqueue(ab)
	ctrpkg.SendCTRNotices(notices)
	if len(aw.writes) != 1 || len(bw.writes) != 0 || len(a.Rooms) != 1 || len(b.Rooms) != 0 {
		t.Fatal("match or push crossed instances")
	}
	architectureFrames(t, aa.Conn.(*lobbyConn), aw, 0)
	notices = b.Enqueue(bb)
	ctrpkg.SendCTRNotices(notices)
	if len(aw.writes) != 1 || len(bw.writes) != 1 || len(b.Rooms) != 1 {
		t.Fatal("second instance failed to form its own room")
	}
	architectureFrames(t, ba.Conn.(*lobbyConn), bw, 0)
	a.Leave(aa.Conn, 0)
	if len(a.Rooms) != 0 || len(b.Rooms) != 1 {
		t.Fatal("disconnect crossed instances")
	}
}

func TestInstanceContextRegistriesAreIsolated(t *testing.T) {
	a, b := lobbypkg.NewSessionRegistry(), lobbypkg.NewSessionRegistry()
	id := [8]byte{1}
	a.Sessions[id] = &lobbypkg.MmSession{Id: id, Owner: 17, OwnerPID: 17}
	b.Sessions[id] = &lobbypkg.MmSession{Id: id, Owner: 17, OwnerPID: 17}
	a.DropSessionsOf(17)
	if a.Sessions[id].Owner != 0 || b.Sessions[id].Owner != 17 {
		t.Fatal("registry disconnect crossed instances")
	}
}

var testDiagnostics = &diagnosticspkg.Diagnostics{RedactLogs: true, DumpRoot: "off", RunStamp: time.Now().Format("20060102-150405")}

var testFiles = &diablopkg.PublisherFiles{Diagnostics: testDiagnostics, PubDir: "pubfiles", PubConfigPath: "pubfiles.json", RiftDir: "riftdata"}

var testOptions = &lobbypkg.LobbyOptions{PublicHost: "127.0.0.1"}

var testRelay = relaypkg.NewRelayTable(testDiagnostics, "", "")

var testConfig = func() configpkg.ServerConfig {
	c, err := configpkg.LoadConfig(func(string) string { return "" }, nil, false)
	if err != nil {
		panic(err)
	}
	return c
}()

var testNAT = &natpkg.NatServer{}

var testTelemetry = dashboardpkg.NewTelemetry(testConfig, testNAT.NatIntros.Load)

func testApplyConfig(c configpkg.ServerConfig) {
	testConfig = c
	testDiagnostics.RedactLogs, testDiagnostics.Verbose, testDiagnostics.DumpRoot = c.RedactLogs, c.Verbose, c.Dumps
	testFiles.PubDir, testFiles.PubConfigPath, testFiles.RiftDir = c.PubfilesDir, c.PubfilesConfig, c.RiftData
	testOptions.MigrateEnabled, testOptions.PublicHost = c.Migrate, c.PublicIP
	testRelay.BindAddr, testRelay.Pids = c.BindAddr, c.RelayPIDs
}

func TestComposedInstancesKeepEqualIdentitiesIndependent(t *testing.T) {
	var instances []*serverInstance
	var lobbies []*lobbyConn
	var wires []*architectureWire
	for n, text := range []string{"first", "second"} {
		cfg, err := configpkg.LoadConfig(lookupFrom(map[string]string{"CTR_NEWS": text, "CTR_NEWS_IT": text}), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DataDir = t.TempDir()
		cfg.StartBalance = int64((n + 1) * 10000)
		database := openTestCTR(t, filepath.Join(cfg.DataDir, storepkg.CtrDB), false)
		instance := newInstance(cfg, database)
		// Mutating the caller's copy cannot rewrite the instance configuration.
		cfg.News.Languages["it"] = configpkg.CtrNewsMessage{}
		w := &architectureWire{}
		l := &lobbyConn{LobbySession: instance.lobby.NewConn(w, ""), lobbyDependencies: lobbyDependencies{files: testFiles, relay: instance.relay, matchmaker: instance.matchmaker, registry: instance.registry, ctr: instance.ctr}}
		fixture, _ := sharedPS4Lobby()
		l.Keys, l.Player = fixture.Keys, fixture.Player
		l.Player.Username = text
		now := time.Now()
		ticket := psnTicket(now)
		instance.tickets.RememberTicket(ticket, *l.Player, now, ps4pkg.TicketWindow)
		if err := instance.presence.RememberPlayerName(*l.Player, now); err != nil {
			t.Fatal(err)
		}
		instance.presence.Online[l.N] = l.Player
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
	if lobbies[0].N != lobbies[1].N || lobbies[0].Pid() != lobbies[1].Pid() {
		t.Fatal("fixture must reuse numeric identities")
	}
	for n, instance := range instances {
		q := demonwarepkg.NewWriter([]byte{12, 3, 10})
		q.Uint32(2)
		q.String("first")
		q.String("second")
		p := architectureReply(t, architectureCall(t, lobbies[n], wires[n], q.Bytes()), 10, 0)
		r := demonwarepkg.NewReader(p[26:])
		pid, _ := r.Uint64()
		name, err := r.String()
		if err != nil || pid != lobbies[n].Pid() || name != lobbies[n].Player.Username || len(r.Remaining()) != 0 {
			t.Fatal("composed name stores crossed")
		}
		a, err := instance.ctr.Economy.Snapshot(pid)
		if err != nil || a.Balances[10] != int64((n+1)*10000) {
			t.Fatal("composed balances crossed")
		}
	}
}
