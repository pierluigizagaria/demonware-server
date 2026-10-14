// Monitoring API for nextendo-dashboard: GET /api/stats?key=<DASH_TOKEN>, with
// the same JSON shape as the NEX game servers (luigis-mansion-3 dashboard.go), so
// the aggregator shows Diablo III without game-specific code. Demonware has no
// RMC or gatherings; the mapping is:
//
//	players     lobby connections whose Nextendo player is known
//	gatherings  bdMatchMaking sessions (21/1); id = first 4 bytes of the session id
//	rmc         remote tasks, named "Service::task" (taskName)
//	sessions    successful auth logins
//
// Also /healthz, and /pubfiles/ (read-only view of the generated publisher files).

package dashboard

import (
	subtle "crypto/subtle"
	binary "encoding/binary"
	json "encoding/json"
	fmt "fmt"
	configpkg "github.com/NextendoNetwork/nextendo-demonware/internal/config"
	diagnosticspkg "github.com/NextendoNetwork/nextendo-demonware/internal/diagnostics"
	"github.com/NextendoNetwork/nextendo-demonware/internal/httpserver"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	net "net"
	http "net/http"
	sort "sort"
	strconv "strconv"
	sync "sync"
	atomic "sync/atomic"
	time "time"
)

type Telemetry struct {
	dashStart                               time.Time
	LoginsTotal, tasksTotal, gatheringsMade atomic.Int64
	statsMu                                 sync.Mutex
	ConnStats                               map[uint64]*ConnStat
	taskEvents                              []taskEvent
	taskCount                               map[string]int64
	peakConnected                           int
	natIntros                               func() int64
	authPort, natPort, dashPort             int
	sniHost, dashToken, bindAddr            string
}

func NewTelemetry(cfg configpkg.ServerConfig, natIntros func() int64) *Telemetry {
	return &Telemetry{dashStart: time.Now(), ConnStats: map[uint64]*ConnStat{}, taskCount: map[string]int64{}, natIntros: natIntros, authPort: cfg.AuthPort, natPort: cfg.NatPort, dashPort: cfg.DashPort, sniHost: cfg.SniHost, dashToken: cfg.DashToken, bindAddr: cfg.BindAddr}
}

type ConnStat struct {
	addr       string
	First      time.Time
	Last       time.Time
	calls      int64
	lastAction string
}

type taskEvent struct {
	t      time.Time
	pid    uint64
	action string
}

func (monitor *Telemetry) RegisterConn(conn uint64, address string) {
	now := time.Now()
	monitor.statsMu.Lock()
	monitor.ConnStats[conn] = &ConnStat{addr: address, First: now, Last: now}
	monitor.statsMu.Unlock()
}

func (monitor *Telemetry) UnregisterConn(conn uint64) {
	monitor.statsMu.Lock()
	delete(monitor.ConnStats, conn)
	monitor.statsMu.Unlock()
}

// noteTask records one remote task for the dashboard (services.go onTask).
func (monitor *Telemetry) NoteTask(conn, pid uint64, service, task byte) {
	name := TaskName(service, task)
	monitor.tasksTotal.Add(1)
	if service == 21 && task == 1 {
		monitor.gatheringsMade.Add(1)
	}
	now := time.Now()
	monitor.statsMu.Lock()
	if cs := monitor.ConnStats[conn]; cs != nil {
		cs.calls++
		cs.Last = now
		cs.lastAction = name
	}
	monitor.taskEvents = append(monitor.taskEvents, taskEvent{t: now, pid: pid, action: name})
	if len(monitor.taskEvents) > 100 {
		monitor.taskEvents = monitor.taskEvents[len(monitor.taskEvents)-100:]
	}
	monitor.taskCount[name]++
	monitor.statsMu.Unlock()
}

// Service and task names as identified from the game binary (service map).
var serviceNames = map[byte]string{
	4: "bdStats", 6: "bdMessaging", 10: "bdStorage", 12: "bdTitleUtilities",
	21: "bdMatchMaking", 23: "bdCounter", 27: "bdDML", 29: "UserData",
	67: "bdEventLog", 68: "bdRichPresence", 104: "bdMarketingComms",
}

var taskNames = map[[2]byte]string{
	{10, 10}: "uploadFile", {10, 21}: "getPublisherFile",
	{12, 6}: "getServerTime", {12, 9}: "getUserNames",
	{68, 3}: "setRichPresence", {68, 4}: "getRichPresence", {68, 5}: "getAndSubscribeRichPresence", {68, 7}: "unsubscribeRichPresence",
	{21, 1}: "createSession", {21, 2}: "updateSession", {21, 3}: "deleteSession",
	{21, 5}: "findSessions", {21, 12}: "updateSessionPlayers",
	{29, 1}: "set", {29, 4}: "get", {29, 11}: "query",
	{104, 4}: "reportMessagesViewed", {104, 5}: "getMessagesForLocations",
}

func TaskName(service, task byte) string {
	sn := serviceNames[service]
	if sn == "" {
		sn = fmt.Sprintf("Service-%d", service)
	}
	tn := taskNames[[2]byte{service, task}]
	if tn == "" {
		tn = fmt.Sprintf("t%d", task)
	}
	return sn + "::" + tn
}

type apiPlayer struct {
	PID        uint64 `json:"pid"`
	Name       string `json:"name"`
	IP         string `json:"ip"`
	State      string `json:"state"`
	Gathering  uint32 `json:"gathering"`
	OnlineSecs int    `json:"onlineSeconds"`
	Calls      int64  `json:"calls"`
	LastAction string `json:"lastAction"`
	IdleSecs   int    `json:"idleSeconds"`
	VR         uint32 `json:"vr"`
	Mode       string `json:"mode"`
	IsHost     bool   `json:"isHost"`
}

type ApiLobbyP struct {
	PID  uint64 `json:"pid"`
	Name string `json:"name"`
	VR   uint32 `json:"vr"`
	Host bool   `json:"host"`
}

type ApiGathering struct {
	ID       uint32      `json:"id"`
	HostPID  uint64      `json:"hostPid"`
	HostName string      `json:"hostName"`
	Type     string      `json:"type"`
	Mode     uint32      `json:"mode"`
	VR       uint32      `json:"vr"`
	Players  []ApiLobbyP `json:"players"`
	Count    int         `json:"count"`
	Max      uint16      `json:"max"`
	State    string      `json:"state"`
	Code     string      `json:"code,omitempty"`
}

type apiEvent struct {
	Ago    int    `json:"agoSeconds"`
	PID    uint64 `json:"pid"`
	Action string `json:"action"`
}

type apiMethod struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type apiServer struct {
	AccessKey  string `json:"accessKey"`
	NexVersion string `json:"nexVersion"`
	AuthPort   string `json:"authPort"`
	SecurePort int    `json:"securePort"`
	SNIHost    string `json:"sniHost"`
	SessionKey int    `json:"sessionKeyLen"`
	Stack      string `json:"stack"`
}

type ApiStats struct {
	ServerTime     string         `json:"serverTime"`
	UptimeSeconds  int            `json:"uptimeSeconds"`
	Connected      int            `json:"connected"`
	InLobby        int            `json:"inLobby"`
	ActiveLobbies  int            `json:"activeLobbies"`
	TotalSessions  int64          `json:"totalSessions"`
	TotalRMC       int64          `json:"totalRmc"`
	GatheringsMade int64          `json:"gatheringsMade"`
	PeakConnected  int            `json:"peakConnected"`
	NATIntros      int64          `json:"natIntroductions"`
	Server         apiServer      `json:"server"`
	Players        []apiPlayer    `json:"players"`
	Gatherings     []ApiGathering `json:"gatherings"`
	Events         []apiEvent     `json:"events"`
	Methods        []apiMethod    `json:"methods"`
}

func (monitor *Telemetry) BuildStats(views DashboardViews) ApiStats {
	return monitor.BuildGameStats(views, "")
}

func (monitor *Telemetry) BuildGameStats(views DashboardViews, game string) ApiStats {
	who := views.Online(game)
	snaps := views.Sessions()

	hosting := map[uint64]ApiGathering{} // host connection -> its public game
	gs := make([]ApiGathering, 0, len(snaps))
	for _, s := range snaps {
		host, live := who[s.Owner]
		if !live || host.PID == 0 || ps4pkg.IsCTRTitle(host.Title) {
			continue
		}
		state := "searching"
		if s.NumPlayers >= 2 {
			state = "matched"
		}
		g := ApiGathering{
			ID: binary.LittleEndian.Uint32(s.Id[:4]), HostPID: host.PID, HostName: host.Username,
			Type: fmt.Sprintf("Public game (type %d)", s.GameType), Mode: s.GameType,
			Players: []ApiLobbyP{{PID: host.PID, Name: host.Username, Host: true}},
			Count:   int(s.NumPlayers), Max: uint16(s.MaxPlayers), State: state,
		}
		hosting[s.Owner] = g
		gs = append(gs, g)
	}
	// CTR legacy session adverts are per-player presence, not actual rooms.
	// Use host-confirmed membership, excluding pending reservations.
	ctrMembership := map[uint64]ApiGathering{}
	if game != "d3" {
		names := map[uint64]string{}
		for _, p := range who {
			names[p.PID] = p.Username
		}
		rooms, membership := views.Rooms(names)
		gs = append(gs, rooms...)
		ctrMembership = membership
	}
	sort.Slice(gs, func(i, j int) bool { return gs[i].ID < gs[j].ID })

	monitor.statsMu.Lock()
	defer monitor.statsMu.Unlock()

	// A player who reconnected can hold two lobby connections: keep the most
	// recently active one.
	byPID := map[uint64]apiPlayer{}
	for n, cs := range monitor.ConnStats {
		p, ok := who[n]
		if !ok || p.PID == 0 {
			continue
		}
		pl := apiPlayer{
			PID: p.PID, Name: p.Username, IP: cs.addr, State: "online",
			OnlineSecs: int(time.Since(cs.First).Seconds()), Calls: cs.calls,
			LastAction: cs.lastAction, IdleSecs: int(time.Since(cs.Last).Seconds()),
		}
		if g, hosts := hosting[n]; hosts {
			pl.State, pl.Gathering, pl.Mode, pl.IsHost = "in a lobby", g.ID, g.Type, true
		}
		if g, joined := ctrMembership[p.PID]; joined {
			pl.State, pl.Gathering, pl.Mode, pl.IsHost = "in a lobby", g.ID, g.Type, p.PID == g.HostPID
		}
		if prev, dup := byPID[p.PID]; dup && prev.IdleSecs <= pl.IdleSecs {
			continue
		}
		byPID[p.PID] = pl
	}
	players := make([]apiPlayer, 0, len(byPID))
	inLobby := 0
	for _, pl := range byPID {
		if pl.Gathering != 0 {
			inLobby++
		}
		players = append(players, pl)
	}
	sort.Slice(players, func(i, j int) bool { return players[i].PID < players[j].PID })
	if len(players) > monitor.peakConnected {
		monitor.peakConnected = len(players)
	}

	ev := make([]apiEvent, 0, len(monitor.taskEvents))
	for i := len(monitor.taskEvents) - 1; i >= 0; i-- {
		e := monitor.taskEvents[i]
		ev = append(ev, apiEvent{Ago: int(time.Since(e.t).Seconds()), PID: e.pid, Action: e.action})
	}
	ms := make([]apiMethod, 0, len(monitor.taskCount))
	for name, c := range monitor.taskCount {
		ms = append(ms, apiMethod{Name: name, Count: c})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Count > ms[j].Count })

	return ApiStats{
		ServerTime:     time.Now().Format("15:04:05"),
		UptimeSeconds:  int(time.Since(monitor.dashStart).Seconds()),
		Connected:      len(players),
		InLobby:        inLobby,
		ActiveLobbies:  len(gs),
		TotalSessions:  monitor.LoginsTotal.Load(),
		TotalRMC:       monitor.tasksTotal.Load(),
		GatheringsMade: monitor.gatheringsMade.Load(),
		PeakConnected:  monitor.peakConnected,
		NATIntros:      monitor.natIntros(),
		Server: apiServer{
			AccessKey:  map[string]string{"ctr": "demonware-ctr", "d3": "demonware-d3"}[game],
			NexVersion: "Demonware lobby 220", AuthPort: fmt.Sprint(monitor.authPort), SecurePort: monitor.natPort,
			SNIHost: monitor.sniHost, SessionKey: 24, Stack: "demonware",
		},
		Players:    players,
		Gatherings: gs,
		Events:     ev,
		Methods:    ms,
	}
}

func (monitor *Telemetry) StartDashboard(views DashboardViews, diag *diagnosticspkg.Diagnostics) (*httpserver.Server, error) {
	port, token := strconv.Itoa(monitor.dashPort), monitor.dashToken

	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if token != "" && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(token)) == 1 {
			return true
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(monitor.BuildGameStats(views, r.URL.Query().Get("game")))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })

	diag.DiagnosticLogf("[D3 Dashboard] stats API on :%s (token=%v)", port, token != "")
	ln, err := net.Listen("tcp", net.JoinHostPort(monitor.bindAddr, port))
	if err != nil {
		return nil, err
	}
	return httpserver.Start(&http.Server{Handler: mux}, ln, false), nil
}
