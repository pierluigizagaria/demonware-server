package main

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

// Legacy fixtures share this explicit test owner. Production has no fallback
// ticket directory; auth, lobby and Umbrella receive the same instance owner.
var testTickets = newTicketStore()

func testAuthHandler(p publisherPlatform, s *ctrAuthSigner, dumps string) http.Handler {
	return newPlatformAuthHandler(testDiagnostics, testTelemetry, testTickets, testPresence, p, s, dumps)
}

func testUmbrellaHandler(w http.ResponseWriter, r *http.Request, body []byte, n uint64, dumps string) {
	handleCTRUmbrella(testDiagnostics, testTickets, w, r, body, n, dumps)
}

func TestInstanceTicketDirectoriesAreIsolated(t *testing.T) {
	a, b := newTicketStore(), newTicketStore()
	now := time.Now()
	ticket := psnTicket(now)
	a.rememberTicket(ticket, playerID{PID: 17, Username: "first", Title: 5760}, now, ticketWindow)
	if _, ok := b.issuedTicketPlayer(ticket, now, ticketWindow); ok {
		t.Fatal("another instance admitted a foreign ticket")
	}
	b.rememberTicket(ticket, playerID{PID: 17, Username: "second", Title: 5760}, now, ticketWindow)
	first, okA := a.issuedIdentity(ticket)
	second, okB := b.issuedIdentity(ticket)
	if !okA || !okB || first.Username != "first" || second.Username != "second" {
		t.Fatal("same numeric identity crossed instance directories")
	}
}

var testPresence = newPresenceStore(nil)

func init() { testMatchmaker.onlinePIDs = testPresence.onlinePIDs }
func testBuildStats() apiStats {
	return testTelemetry.buildStats(testPresence, testMatchmaker, testRegistry)
}
func testBuildGameStats(game string) apiStats {
	return testTelemetry.buildGameStats(testPresence, testMatchmaker, testRegistry, game)
}

func TestInstancePresenceAndPersistentNamesAreIsolated(t *testing.T) {
	firstDB := openTestCTR(t, filepath.Join(t.TempDir(), ctrDB), false)
	secondDB := openTestCTR(t, filepath.Join(t.TempDir(), ctrDB), false)
	a, b := newPresenceStore(firstDB), newPresenceStore(secondDB)
	now := time.Now()
	if a.rememberPlayerName(playerID{PID: 17, Username: "first"}, now) != nil || b.rememberPlayerName(playerID{PID: 17, Username: "second"}, now) != nil {
		t.Fatal("record names")
	}
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.presence, second.presence = a, b
	first.player.Username, second.player.Username = "first", "second"
	a.online[first.n], b.online[second.n] = first.player, second.player
	aw, bw := &architectureWire{}, &architectureWire{}
	first.c, second.c = aw, bw
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
	if !bytes.Equal(a.rich[17].data, []byte{1}) || !bytes.Equal(b.rich[17].data, []byte{2}) {
		t.Fatal("presence crossed instance boundary")
	}
	a.dropOnline(first.n)
	if len(a.rich) != 0 || b.nicknameOnline(17) != "second" || len(b.rich) != 1 {
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

var testReplies = &replyCounter{}

func TestInstanceReplyCountersAreIsolated(t *testing.T) {
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.replyCounter, second.replyCounter = &replyCounter{}, &replyCounter{}
	for _, l := range []*lobbyConn{first, second, first} {
		w := &architectureWire{}
		l.c = w
		frames := architectureCall(t, l, w, []byte{255, 3, 1})
		payload, err := hex.DecodeString(frames[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := demonware.NewReader(payload).Uint64()
		if err != nil || transaction != l.transactions.Load() {
			t.Fatal("unexpected transaction counter")
		}
	}
	if first.transactions.Load() != 2 || second.transactions.Load() != 1 {
		t.Fatal("transaction counters crossed instance boundary")
	}
}

var testStartBalance int64
var testCTR = &ctrServices{economy: &economyStore{initialBalance: func() int64 { return testStartBalance }}, stats: &ctrStatsStore{}}

func testLoadWallets() { testCTR.economy.loadWallets(testConfig.dataDir) }

func init() {
	testCTR.stats.(*ctrStatsStore).fallbackDir = func() string { return testCTR.economy.dir }
}

func TestInstanceEconomyAndLeaderboardAreIsolated(t *testing.T) {
	a, aDB := newTestEconomy(t, false)
	b, bDB := newTestEconomy(t, false)
	aDB.initialBalance = func() int64 { return 10000 }
	bDB.initialBalance = func() int64 { return 20000 }
	first, _ := sharedPS4Lobby()
	second, _ := sharedPS4Lobby()
	first.ctr = &ctrServices{economy: a, stats: aDB}
	second.ctr = &ctrServices{economy: b, stats: bDB}
	first.c = &architectureWire{}
	second.c = &architectureWire{}
	for _, l := range []*lobbyConn{first, second} {
		frames := architectureCall(t, l, l.c.(*architectureWire), marketplaceHex(t, ps4BalancesRequest))
		payload, _ := hex.DecodeString(frames[0].Payload)
		want := getPlayerBalancesResponse(map[uint64]int64{10: l.ctr.economy.backend.(*ctrSQLite).startingBalance()}, 3)
		if !bytes.Equal(structBodyOf(t, payload), want) {
			t.Fatal("balance crossed instance boundary")
		}
	}
	architectureCall(t, first, first.c.(*architectureWire), marketplaceHex(t, ps4PurchaseRequest))
	aa, errA := a.snapshot(first.pid())
	bb, errB := b.snapshot(second.pid())
	if errA != nil || errB != nil || aa.Balances[10] >= 10000 || bb.Balances[10] != 20000 || len(bb.Items) != 0 {
		t.Fatal("purchase crossed instance boundary")
	}
	if err := aDB.write([]ctrStatWrite{{ctrStat: ctrStat{Board: 89, PID: first.pid(), Score: 7}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := bDB.read(89)
	if err != nil || len(rows) != 0 {
		t.Fatal("leaderboard crossed instance boundary")
	}
}

var testMatchmaker = &ctrMatchmaker{rooms: map[uint64]*ctrRoom{}}
var testRegistry = newSessionRegistry()

func TestInstanceMatchmakingAndPushesAreIsolated(t *testing.T) {
	a := &ctrMatchmaker{onlinePIDs: func() []uint64 { return []uint64{17, 18} }, rooms: map[uint64]*ctrRoom{}}
	b := &ctrMatchmaker{onlinePIDs: func() []uint64 { return []uint64{17, 18} }, rooms: map[uint64]*ctrRoom{}}
	prepare := func(m *ctrMatchmaker, pid uint64) (*ctrSearch, *architectureWire) {
		s := m.testCTRSearch(t, pid, 0)
		l, _ := sharedPS4Lobby()
		l.n, l.player.PID, l.matchmaker = pid, pid, m
		w := &architectureWire{}
		l.c, s.conn = w, l
		return s, w
	}
	aa, aw := prepare(a, 17)
	ba, bw := prepare(b, 17)
	ab, _ := prepare(a, 18)
	bb, _ := prepare(b, 18)
	if aa.id != ba.id || ab.id != bb.id {
		t.Fatal("instance search ID sequences differ")
	}
	a.enqueue(aa)
	b.enqueue(ba)
	notices := a.enqueue(ab)
	sendCTRNotices(notices)
	if len(aw.writes) != 1 || len(bw.writes) != 0 || len(a.rooms) != 1 || len(b.rooms) != 0 {
		t.Fatal("match or push crossed instances")
	}
	architectureFrames(t, aa.conn, aw, 0)
	notices = b.enqueue(bb)
	sendCTRNotices(notices)
	if len(aw.writes) != 1 || len(bw.writes) != 1 || len(b.rooms) != 1 {
		t.Fatal("second instance failed to form its own room")
	}
	architectureFrames(t, ba.conn, bw, 0)
	a.leave(aa.conn, 0)
	if len(a.rooms) != 0 || len(b.rooms) != 1 {
		t.Fatal("disconnect crossed instances")
	}
}

func TestInstanceContextRegistriesAreIsolated(t *testing.T) {
	a, b := newSessionRegistry(), newSessionRegistry()
	id := [8]byte{1}
	a.sessions[id] = &mmSession{id: id, owner: 17, ownerPID: 17}
	b.sessions[id] = &mmSession{id: id, owner: 17, ownerPID: 17}
	a.dropSessionsOf(17)
	if a.sessions[id].owner != 0 || b.sessions[id].owner != 17 {
		t.Fatal("registry disconnect crossed instances")
	}
}

var testDiagnostics = &diagnostics{redactLogs: true, dumpRoot: "off", runStamp: time.Now().Format("20060102-150405")}
var testFiles = &publisherFiles{diagnostics: testDiagnostics, pubDir: "pubfiles", pubConfigPath: "pubfiles.json", riftDir: "riftdata"}
var testOptions = &lobbyOptions{publicHost: "127.0.0.1"}
var testRelay = newRelayTable(testDiagnostics, "", "")

var testConfig = func() serverConfig {
	c, err := loadConfig(func(string) string { return "" }, nil, false)
	if err != nil {
		panic(err)
	}
	return c
}()
var testNAT = &natServer{}
var testTelemetry = newTelemetry(testConfig, testNAT.natIntros.Load)

func testApplyConfig(c serverConfig) {
	testConfig = c
	testDiagnostics.redactLogs, testDiagnostics.verbose, testDiagnostics.dumpRoot = c.redactLogs, c.verbose, c.dumps
	testFiles.pubDir, testFiles.pubConfigPath, testFiles.riftDir = c.pubfilesDir, c.pubfilesConfig, c.riftData
	testOptions.migrateEnabled, testOptions.publicHost = c.migrate, c.publicIP
	testRelay.bindAddr, testRelay.pids = c.bindAddr, c.relayPIDs
}

func TestComposedInstancesKeepEqualIdentitiesIndependent(t *testing.T) {
	var instances []*serverInstance
	var lobbies []*lobbyConn
	var wires []*architectureWire
	for n, text := range []string{"first", "second"} {
		cfg, err := loadConfig(lookupFrom(map[string]string{"CTR_NEWS": text, "CTR_NEWS_IT": text}), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg.dataDir = t.TempDir()
		cfg.startBalance = int64((n + 1) * 10000)
		database := openTestCTR(t, filepath.Join(cfg.dataDir, ctrDB), false)
		instance := newInstance(cfg, database)
		// Mutating the caller's copy cannot rewrite the instance configuration.
		cfg.news.languages["it"] = ctrNewsMessage{}
		w := &architectureWire{}
		l := instance.lobby.newConn(w, "")
		fixture, _ := sharedPS4Lobby()
		l.keys, l.player = fixture.keys, fixture.player
		l.player.Username = text
		now := time.Now()
		ticket := psnTicket(now)
		instance.tickets.rememberTicket(ticket, *l.player, now, ticketWindow)
		if err := instance.presence.rememberPlayerName(*l.player, now); err != nil {
			t.Fatal(err)
		}
		instance.presence.online[l.n] = l.player
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
	if lobbies[0].n != lobbies[1].n || lobbies[0].pid() != lobbies[1].pid() {
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
		if err != nil || pid != lobbies[n].pid() || name != lobbies[n].player.Username || len(r.Remaining()) != 0 {
			t.Fatal("composed name stores crossed")
		}
		a, err := instance.ctr.economy.snapshot(pid)
		if err != nil || a.Balances[10] != int64((n+1)*10000) {
			t.Fatal("composed balances crossed")
		}
	}
}
