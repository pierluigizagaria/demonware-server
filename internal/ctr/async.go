package ctr

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

// Peer is the engine's connection identity and notification seam. A PID
// alone cannot distinguish a stale connection from its replacement.
type Peer interface {
	ConnID() uint64
	PID() uint64
	Logf(string, ...any)
	SendPush(uint32, func(*demonware.Writer)) error
}

// Event layouts verified against CTR 1.0.15's bdAsyncMatchMakingEventHandler.
const (
	EventJoinLobby   = 2001
	EventCreateLobby = 2005
	EventUpdateLobby = 2006
	EventMergeLobby  = 2008
)

type SearchDoc struct {
	Members []uint64 `json:"members"`
	Slots   struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"lobby_slots"`
	Rules struct {
		Filter map[string]json.RawMessage `json:"filter"`
	} `json:"ruleset_payload"`
}

type PlayerInfo struct {
	Listen struct {
		Address string `json:"local_address"`
		ID      string `json:"security_id"`
		Key     string `json:"security_key"`
	} `json:"listen_server"`
}

type Search struct {
	Conn     Peer
	ID, PID  uint64
	Doc      SearchDoc
	info     PlayerInfo
	Filter   string
	filterID string

	// Seats this search needs: one per party member. Only the party leader sends
	// the search, so counting searches instead of members would sit a party of
	// three in a single seat and overfill the lobby.
	Seats int
}

// Keep membership and its seat count together for host-reported failures and
// platform-reported disconnects. The source search remains an immutable snapshot.
func (s *Search) removeMembers(remove func(uint64) bool) *Search {
	copy := *s
	copy.Doc.Members = make([]uint64, 0, len(s.Doc.Members))
	for _, pid := range s.Doc.Members {
		if !remove(pid) {
			copy.Doc.Members = append(copy.Doc.Members, pid)
		}
	}
	copy.Seats = len(copy.Doc.Members)
	return &copy
}

type Room struct {
	clock       func() time.Time
	ID, Version uint64
	created     int64
	Host        *Search
	Players     []*Search
	HostDoc     map[string]any
	Capacity    int
	notified    map[uint64]bool
	MergeSource *Room
	MergeTarget uint64
}

type Matchmaker struct {
	ids        atomic.Uint64
	OnlinePIDs func() []uint64
	Clock      func() time.Time
	mu         sync.Mutex
	waiting    []*Search
	rooms      map[uint64]*Room
	// Searches accepted but not yet matched, by search ID: their reply is
	// still being sent. They hold their members' seats but are not offered to
	// anyone. A follower's own search may shrink one before it is activated.
	pending map[uint64]*Search
}

// NewMatchmaker initializes an instance owner without starting workers.
func NewMatchmaker(onlinePIDs func() []uint64) *Matchmaker {
	return &Matchmaker{OnlinePIDs: onlinePIDs, rooms: map[uint64]*Room{}}
}

func (m *Matchmaker) ParseSearch(l Peer, raw, playerInfo string) (*Search, error) {
	s := &Search{Conn: l, PID: l.PID()}
	// A party searches as ONE request issued by its leader, listing every member.
	// Refusing anything but a lone member meant a duo or trio never entered the
	// queue at all: they watched a lobby wait for players and were never placed
	// in it. The leader must be among the members it claims.
	if json.Unmarshal([]byte(raw), &s.Doc) != nil || s.PID == 0 || len(s.Doc.Members) < 1 || len(s.Doc.Members) > 8 || s.Doc.Slots.Min < 1 || s.Doc.Slots.Max < s.Doc.Slots.Min || s.Doc.Slots.Max > 8 || len(s.Doc.Rules.Filter) == 0 {
		return nil, errors.New("invalid search or unsupported party membership")
	}
	// The leader must be among the members it claims to be searching for.
	leader := false
	seen := make(map[uint64]bool)
	for _, pid := range s.Doc.Members {
		if pid == 0 || seen[pid] {
			return nil, errors.New("zero or duplicate party member")
		}
		seen[pid] = true
		if pid == s.PID {
			leader = true
		}
	}
	if !leader || len(s.Doc.Members) > s.Doc.Slots.Max {
		return nil, errors.New("invalid search or unsupported party membership")
	}
	s.Seats = len(s.Doc.Members)
	if json.Unmarshal([]byte(playerInfo), &s.info) != nil {
		return nil, errors.New("missing player info")
	}
	for encoded, n := range map[string]int{s.info.Listen.Address: 67, s.info.Listen.ID: 8, s.info.Listen.Key: 16} {
		b, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(b) != n {
			return nil, errors.New("invalid player address or DTLS data")
		}
	}
	filter, _ := json.Marshal(s.Doc.Rules.Filter)
	s.Filter = string(filter)
	// Players only match when their filters are byte-identical, so a filter that
	// carries anything player-specific silently fragments matchmaking into pairs.
	// The fingerprint makes that visible without logging the payload itself.
	sum := sha256.Sum256(filter)
	s.filterID = hex.EncodeToString(sum[:4])
	s.ID = m.ids.Add(1)
	return s, nil
}

type notice struct {
	Conn          Peer
	Event         uint32
	IDs           []uint64
	Host, Backend string
}

func (n notice) Write(w *demonware.Writer) {
	for _, id := range n.IDs {
		w.Uint64(id)
	}
	w.String(n.Host)
	w.String(n.Backend)
}

func SendNotices(notices []notice) {
	for _, n := range notices {
		if err := n.Conn.SendPush(n.Event, n.Write); err != nil {
			n.Conn.Logf("[ctr] matchmaking push event=%d failed: %v", n.Event, err)
		} else {
			n.Conn.Logf("[ctr] matchmaking push event=%d", n.Event)
		}
	}
}

// Occupancy counts PEOPLE in the room, not searches: one search may be a party.
func (r *Room) Occupancy() int {
	n := 0
	for _, p := range r.Players {
		for _, pid := range p.Doc.Members {
			if !r.failed(pid) {
				n++
			}
		}
	}
	if r.MergeSource != nil {
		n += r.MergeSource.Occupancy()
	}
	return n
}

// CTR updateLobbyPlayerStatus sets state 2 for an actual peer, and state 3
// when an expected peer times out. A failed join must not reserve a seat forever.
func (r *Room) state(pid uint64) int {
	states, _ := r.HostDoc["player_state"].(map[string]any)
	v, _ := states[strconv.FormatUint(pid, 10)].(json.Number)
	n, _ := v.Int64()
	return int(n)
}

func (r *Room) failed(pid uint64) bool {
	return pid != r.Host.PID && r.state(pid) == 3
}

func (r *Room) connected() int {
	n := 0
	for _, p := range r.Players {
		for _, pid := range p.Doc.Members {
			if r.state(pid) == 2 {
				n++
			}
		}
	}
	return n
}

// Includes seats reserved by the existing merge engine, before the handoff.
func (r *Room) expectedSearches() []*Search {
	players := append([]*Search(nil), r.Players...)
	if r.MergeSource != nil {
		players = append(players, r.MergeSource.Players...)
	}
	return players
}

func (r *Room) Documents() (string, string) {
	expected := map[string]string{}
	for _, p := range r.expectedSearches() {
		// Every member of a party is expected, not just the leader who searched.
		for _, pid := range p.Doc.Members {
			if r.failed(pid) {
				continue
			}
			expected[strconv.FormatUint(pid, 10)] = "expected"
		}
	}
	backend := map[string]any{
		"update_id": r.Version, "update_time": r.now().Unix(), "lobby_id": r.ID, "create_time": r.created,
		"players": expected, "dtls": map[string]string{"security_id": r.Host.info.Listen.ID, "security_key": r.Host.info.Listen.Key}, "ruleset_payload": r.Host.Doc.Rules,
	}
	h, _ := json.Marshal(r.HostDoc)
	b, _ := json.Marshal(backend)
	return string(h), string(b)
}

func (r *Room) notice(p *Search, event uint32) notice {
	h, b := r.Documents()
	ids := []uint64{p.ID, r.ID}
	if event == EventCreateLobby {
		ids = append(ids, r.Version)
	}
	if event == EventUpdateLobby {
		ids = []uint64{r.ID, r.Version}
	}
	return notice{p.Conn, event, ids, h, b}
}

// Party members are the leader's claim: the server has no evidence of who
// agreed to join. A claim therefore never displaces another player's seat.
// claimConflict reports a claimed follower who already holds a seat in a
// search not led by this leader (waiting, pending, or in a room).
func (m *Matchmaker) claimConflict(s *Search) bool {
	claimed := func(p *Search) bool {
		if p == s || p.PID == s.PID {
			return false // the leader's own searches are replaced by this one
		}
		for _, pid := range s.Doc.Members {
			if pid != s.PID && slices.Contains(p.Doc.Members, pid) {
				return true
			}
		}
		return false
	}
	if slices.ContainsFunc(m.waiting, claimed) {
		return true
	}
	for _, p := range m.pending {
		if claimed(p) {
			return true
		}
	}
	for _, r := range m.rooms {
		if claimed(r.Host) || slices.ContainsFunc(r.Players, claimed) {
			return true
		}
	}
	return false
}

// Reserve takes ownership of the search; callers must not mutate it after acceptance.
// It accepts a search before its success reply is sent: the conflict
// check and the seat reservation happen together, so two parties cannot both
// be told they claimed the same player. Activate matches it after the reply;
// Release drops it if the reply could not be sent.
func (m *Matchmaker) Reserve(s *Search) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.claimConflict(s) {
		return errors.New("a claimed party member already holds a seat")
	}
	if m.pending == nil {
		m.pending = map[uint64]*Search{}
	}
	m.pending[s.ID] = s
	return nil
}

func (m *Matchmaker) Release(s *Search) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, s.ID)
}

// Enqueue reserves and activates a search in one go.
func (m *Matchmaker) Enqueue(s *Search) []notice {
	if m.Reserve(s) != nil {
		return nil
	}
	return m.Activate(s)
}

// Activate matches a reserved search in its current membership. A reservation
// withdrawn meanwhile (its leader disconnected or searched again) is not
// revived.
func (m *Matchmaker) Activate(reserved *Search) []notice {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.pending[reserved.ID]
	if s == nil {
		reserved.Conn.Logf("[ctr] search id=%d withdrawn before activation", reserved.ID)
		return nil
	}
	delete(m.pending, s.ID)
	// Retries must not match an account with itself or create duplicate searches.
	// A player who searches again is NOT in a lobby any more, whatever this side
	// still believes. Returning nil here left them stuck forever: they matched
	// once, the client failed to actually join, and every later search was
	// answered with silence because the server still counted them as a member of
	// a room they were never in. Evict them and let them match normally.
	// The leader's own seat elsewhere (as another party's follower) is released
	// too; the other members' seats were checked free by reserve.
	evicted := m.detach(0, func(p *Search) bool { return p.PID == s.PID }, map[uint64]bool{s.PID: true})
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	sort.Slice(rooms, func(i, j int) bool {
		a, b := rooms[i], rooms[j]
		if a.Occupancy() != b.Occupancy() {
			return a.Occupancy() > b.Occupancy()
		}
		return a.ID < b.ID
	})
	for _, r := range rooms {
		if r.MergeTarget != 0 && m.rooms[r.MergeTarget] == nil {
			r.MergeTarget = 0
		}
		open, _ := r.HostDoc["lobby_open"].(bool)
		reason := "selected"
		switch {
		case r.MergeTarget != 0 || r.MergeSource != nil:
			reason = "merge-pending"
		case !open:
			reason = "closed"
		case r.Host.Filter != s.Filter:
			reason = "filter"
		case r.Occupancy()+s.Seats > r.Capacity:
			reason = "room-capacity"
		case r.Occupancy()+s.Seats > s.Doc.Slots.Max:
			reason = "search-capacity"
		}
		s.Conn.Logf("[ctr] candidate search=%d seats=%d filter=%s room=%d room_filter=%s open=%t occupied=%d connected=%d capacity=%d search_max=%d reason=%s", s.ID, s.Seats, s.filterID, r.ID, r.Host.filterID, open, r.Occupancy(), r.connected(), r.Capacity, s.Doc.Slots.Max, reason)
		if reason == "selected" {
			// A retry must not inherit its previous failed state in this room.
			if states, ok := r.HostDoc["player_state"].(map[string]any); ok {
				for _, pid := range s.Doc.Members {
					delete(states, strconv.FormatUint(pid, 10))
				}
			}
			r.Players = append(r.Players, s)
			r.Capacity = min(r.Capacity, s.Doc.Slots.Max)
			r.Version++
			r.HostDoc["update_id"] = r.Version
			r.HostDoc["update_time"] = m.now().Unix()
			return append(evicted, r.notice(r.Host, EventUpdateLobby))
		}
	}
	m.waiting = append(m.waiting, s)
	group := []*Search{}
	seats := 0
	minimum, maximum := 2, 8
	for _, p := range m.waiting {
		if p.Filter != s.Filter {
			continue
		}
		nextMin, nextMax := max(minimum, p.Doc.Slots.Min), min(maximum, p.Doc.Slots.Max)
		if nextMin > nextMax || seats+p.Seats > nextMax {
			continue
		}
		group = append(group, p)
		seats += p.Seats
		minimum, maximum = nextMin, nextMax
	}
	if seats < minimum {
		return evicted
	}
	h := group[0]
	r := &Room{clock: m.now, ID: m.ids.Add(1), Version: 1, created: m.now().Unix(), Host: h, Players: group, Capacity: maximum, notified: map[uint64]bool{h.Conn.ConnID(): true}}
	r.HostDoc = map[string]any{
		"update_id": r.Version, "update_time": r.created, "game_id": r.ID, "lobby_open": true, "lobby_open_for_pres_join": true,
		"player_state":  map[string]uint32{strconv.FormatUint(h.PID, 10): 1},
		"listen_server": lobbyListenServer{HostAddress: h.info.Listen.Address, HostPlayerID: h.PID},
		"team_balance":  lobbyTeamBalance{CanChangeTeams: true}, "ruleset_payload": map[string]any{}, "attachment": nil,
	}
	m.rooms[r.ID] = r
	selected := map[*Search]bool{}
	out := []notice{r.notice(h, EventCreateLobby)}
	for _, p := range group {
		selected[p] = true

	}
	remaining := m.waiting[:0]
	for _, p := range m.waiting {
		if !selected[p] {
			remaining = append(remaining, p)
		}
	}
	m.waiting = remaining
	return append(evicted, out...)
}

func (m *Matchmaker) Cancel(conn Peer, id uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.waiting {
		if p.Conn.ConnID() == conn.ConnID() && p.ID == id {
			m.waiting = append(m.waiting[:i], m.waiting[i+1:]...)
			return true
		}
	}
	return false
}

// Leave handles a lobby disband (id != 0) or a disconnect (id == 0). The
// engine tracks searches by their leader's connection; a party follower who
// leaves loses only its own seat, and a surviving host is told the new
// expected membership. Any merge reservation involving the room is released.
// A disconnect while the same account is connected elsewhere releases no
// follower seat: the newer connection may hold it. (The run loop drops the
// connection's own online entry before it calls leave.)
func (m *Matchmaker) Leave(conn Peer, id uint64) []notice {
	pid := conn.PID()
	stale := id == 0 && pid != 0 && slices.Contains(m.OnlinePIDs(), pid)
	m.mu.Lock()
	defer m.mu.Unlock()
	seats := map[uint64]bool{}
	if pid != 0 && !stale {
		seats[pid] = true
	}
	return m.detach(id, func(p *Search) bool { return p.Conn.ConnID() == conn.ConnID() }, seats)
}

// detach withdraws every search for which lead is true and removes the seats
// of the other listed players from their parties; a search's own leader is
// never removed this way. With id == 0 it covers waiting and pending searches
// and every room; with id != 0 only that room (a disband ends no search). A
// room whose host search is withdrawn closes; a surviving room whose
// membership changes releases any merge reservation and tells its host.
// Closing rooms are settled first, so no notice targets a room that is gone.
// The caller holds m.mu.
func (m *Matchmaker) detach(id uint64, lead func(*Search) bool, seats map[uint64]bool) []notice {
	removable := func(p *Search, pid uint64) bool { return seats[pid] && pid != p.PID }
	member := func(p *Search) bool {
		return slices.ContainsFunc(p.Doc.Members, func(pid uint64) bool { return removable(p, pid) })
	}
	without := func(p *Search) *Search {
		return p.removeMembers(func(pid uint64) bool { return removable(p, pid) })
	}
	if id == 0 {
		kept := m.waiting[:0]
		for _, p := range m.waiting {
			switch {
			case lead(p):
			case member(p):
				kept = append(kept, without(p))
			default:
				kept = append(kept, p)
			}
		}
		m.waiting = kept
		for searchID, p := range m.pending {
			switch {
			case lead(p):
				delete(m.pending, searchID)
			case member(p):
				m.pending[searchID] = without(p)
			}
		}
	}
	var notices []notice
	closing := map[*Room]bool{}
	for roomID, r := range m.rooms {
		if (id == 0 || roomID == id) && lead(r.Host) {
			closing[r] = true
		}
	}
	for r := range closing {
		// Release the other side of a merge reservation; only a surviving
		// destination is told, with the version bump of a cancelled merge.
		if src := r.MergeSource; src != nil {
			src.MergeTarget = 0
			r.MergeSource = nil
		} else if dst := m.rooms[r.MergeTarget]; dst != nil && dst.MergeSource == r {
			if closing[dst] {
				dst.MergeSource, r.MergeTarget = nil, 0
			} else {
				notices = append(notices, m.cancelMerge(dst)...)
			}
		}
		delete(m.rooms, r.ID)
	}
	for roomID, r := range m.rooms {
		if id != 0 && roomID != id {
			continue
		}
		var departed []uint64
		kept := r.Players[:0]
		for _, p := range r.Players {
			switch {
			case lead(p):
				departed = append(departed, p.Doc.Members...)
				delete(r.notified, p.Conn.ConnID())
			case member(p):
				q := without(p)
				for _, pid := range p.Doc.Members {
					if removable(p, pid) {
						departed = append(departed, pid)
					}
				}
				if r.Host == p {
					r.Host = q
				}
				kept = append(kept, q)
			default:
				kept = append(kept, p)
			}
		}
		r.Players = kept
		if len(departed) == 0 {
			continue
		}
		if states, ok := r.HostDoc["player_state"].(map[string]any); ok {
			for _, pid := range departed {
				delete(states, strconv.FormatUint(pid, 10))
			}
		}
		// The departure invalidates a merge reservation; cancelling one in
		// which this room is the destination already bumps and notifies it.
		destination := r.MergeSource != nil
		cancelled := m.cancelRoomMerge(r)
		notices = append(notices, cancelled...)
		if destination && len(cancelled) > 0 {
			continue
		}
		r.Version++
		r.HostDoc["update_id"] = r.Version
		r.HostDoc["update_time"] = m.now().Unix()
		notices = append(notices, r.notice(r.Host, EventUpdateLobby))
	}
	return notices
}

func (m *Matchmaker) Documents(conn Peer, id uint64) (string, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rooms[id]; r != nil {
		for _, p := range r.Players {
			if p.Conn.ConnID() == conn.ConnID() {
				h, b := r.Documents()
				return h, b, true
			}
		}
	}
	return "", "", false
}

func (m *Matchmaker) SyncDoc(conn Peer, id, version uint64, raw string) (bool, []notice) {
	var doc map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&doc) != nil || doc == nil {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[id]
	if r == nil || r.Host.Conn.ConnID() != conn.ConnID() || r.Version != version {
		return false, nil
	}
	for _, key := range []string{"lobby_open", "lobby_open_for_pres_join", "player_state", "listen_server", "team_balance", "ruleset_payload", "attachment"} {
		if _, ok := doc[key]; !ok {
			return false, nil
		}
	}
	// The version tracks MEMBERSHIP and is bumped only when someone joins (see
	// enqueue). It used to be bumped here as well, on every accepted sync, which
	// put the room permanently one ahead of the host: the host echoes the
	// update_id of the document IT composed, so its next sync carried the old
	// number and was refused -- and so was every sync after that.
	//
	// A host that cannot sync cannot CLOSE its lobby either. lobby_open stayed
	// true on the server's original document, so the matchmaker kept sending new
	// players into a lobby that had already started racing and could not take
	// them: "you can only join once, and after it starts nobody else gets in".
	doc["update_id"] = r.Version
	doc["update_time"] = m.now().Unix()
	r.HostDoc = doc
	mergeNotices := m.finishMerge(r)
	// A source may close or withdraw eligibility while the destination is
	// preparing its acknowledgement. Release its reservation immediately.
	if dst := m.rooms[r.MergeTarget]; dst != nil && dst.MergeSource == r &&
		(!mergeOpen(r) || !mergeEligible(r, "is_merge_src")) {
		mergeNotices = append(mergeNotices, m.cancelMerge(dst)...)
	}
	// Remove only members the host explicitly rejected. Keep connected party
	// members, and do not expire a reservation merely because time passed here.
	kept := r.Players[:0]
	released := 0
	for _, p := range r.Players {
		copy := p.removeMembers(r.failed)
		released += len(p.Doc.Members) - copy.Seats
		if copy.Seats == 0 {
			delete(r.notified, p.Conn.ConnID())
			continue
		}
		if p == r.Host {
			r.Host = copy
		}
		kept = append(kept, copy)
	}
	r.Players = kept
	if released > 0 {
		conn.Logf("[ctr] released failed reservations room=%d seats=%d", r.ID, released)
	}
	conn.Logf("[ctr] room-state room=%d version=%d open=%v occupied=%d connected=%d searches=%d", r.ID, r.Version, doc["lobby_open"], r.Occupancy(), r.connected(), len(r.Players))
	// 2006 asks the host to produce a new document; it is not an ACK to
	// broadcast. Echoing it on every sync causes an endless sync/push loop.
	// Release pending guests only after the host has supplied its document.
	out := mergeNotices
	for _, p := range r.Players {
		if !r.notified[p.Conn.ConnID()] {
			r.notified[p.Conn.ConnID()] = true
			out = append(out, r.notice(p, EventJoinLobby))
		}
	}
	// A search already waiting must be reconsidered when the host opens its
	// lobby or reports failed joins. Otherwise it waits for an unrelated new
	// search even though this sync has made suitable seats available.
	// Do not advance the destination version in the same operation as 2008:
	// its handoff documents must remain current when the adapter delivers them.
	if open, _ := doc["lobby_open"].(bool); len(mergeNotices) == 0 && open && r.MergeTarget == 0 && r.MergeSource == nil {
		remaining := m.waiting[:0]
		added := 0
		for _, p := range m.waiting {
			if p.Filter != r.Host.Filter || r.Occupancy()+p.Seats > min(r.Capacity, p.Doc.Slots.Max) {
				remaining = append(remaining, p)
				continue
			}
			if states, ok := doc["player_state"].(map[string]any); ok {
				for _, pid := range p.Doc.Members {
					delete(states, strconv.FormatUint(pid, 10))
				}
			}
			r.Players = append(r.Players, p)
			r.Capacity = min(r.Capacity, p.Doc.Slots.Max)
			added++
			conn.Logf("[ctr] backfill waiting search=%d seats=%d room=%d", p.ID, p.Seats, r.ID)
		}
		m.waiting = remaining
		if added > 0 {
			r.Version++
			doc["update_id"] = r.Version
			out = append(out, r.notice(r.Host, EventUpdateLobby))
		}
	}
	if len(out) == 0 {
		out = append(out, m.StartMerge(r)...)
	}
	return true, out
}

func (m *Matchmaker) now() time.Time {
	if m.Clock != nil {
		return m.Clock()
	}
	return time.Now()
}

func (r *Room) now() time.Time {
	if r.clock != nil {
		return r.clock()
	}
	return time.Now()
}
