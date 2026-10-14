package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

func architectureCall(t *testing.T, l *lobbyConn, w *architectureWire, payload []byte) []architectureFrame {
	t.Helper()
	w.Reset()
	w.writes = nil
	before := l.s2cSeq
	l.onTask(payload)
	frames := architectureFrames(t, l, w, before)
	if l.afterReplyActive {
		t.Fatal("callback retained after reply attempt")
	}
	return frames
}

func architectureReply(t *testing.T, frames []architectureFrame, task byte, code uint32) []byte {
	t.Helper()
	if len(frames) == 0 || frames[0].Inner != demonware.InnerTaskReply {
		t.Fatal("reply missing or push sent first")
	}
	p, _ := hex.DecodeString(frames[0].Payload)
	marketplaceOutcome(t, p, code)
	if code == 0 && (len(p) < 16 || p[14] != 3 || p[15] != task) {
		t.Fatal("reply task changed")
	}
	return p
}

func architectureEventRequest(e achievementEvent) []byte {
	event := demonware.AppendBytesField(nil, 1, []byte(e.Name))
	event = demonware.AppendVarintField(event, 2, e.Timestamp)
	var keys []string
	for k := range e.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kv := demonware.AppendBytesField(nil, 1, []byte(k))
		kv = demonware.AppendVarintField(kv, 2, e.Values[k])
		event = demonware.AppendBytesField(event, 3, kv)
	}
	w := demonware.NewWriter([]byte{125, 3, 1})
	w.Struct(demonware.AppendBytesField(demonware.AppendBytesField(nil, 1, []byte("octane_psn")), 2, event))
	return w.Bytes()
}

func TestArchitectureRewardReplyFailureStillCommitsAndPushes(t *testing.T) {
	for _, fail := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			l, w, db := architectureBaseline(t)
			// The timestamp is an input to receipts and multiplier/validity rules;
			// freeze the component clock instead of changing/normalizing that input.
			now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			l.clock = func() time.Time { return now }
			db.clock = l.clock
			event := capturedRace(now)
			payload := architectureEventRequest(event)
			w.fail = fail
			frames := architectureCall(t, l, w, payload)
			reply := architectureReply(t, frames, 1, 0)
			if len(structBodyOf(t, reply)) != 0 || len(frames) != 2 || frames[1].Inner != demonware.InnerPush {
				t.Fatal("achievement reply/push changed")
			}
			push, _ := hex.DecodeString(frames[1].Payload)
			want := demonware.NewWriter(nil)
			want.Uint32(76)
			want.Struct(balanceUpdatePayload(l.pid(), 10027))
			if !bytes.Equal(push, want.Bytes()) {
				t.Fatal("reward balance push changed")
			}
			var receipt string
			var recorded int64
			if err := db.db.QueryRow("SELECT receipt, recorded_at FROM race_receipts").Scan(&receipt, &recorded); err != nil || receipt != raceReceipt(event) || recorded != now.Unix() {
				t.Fatal("reward timestamp/receipt not persisted exactly")
			}
			a, err := db.load(l.pid(), false)
			if err != nil || a.Balances[10] != 10027 || !a.RaceReceipts[receipt] {
				t.Fatal("failed reply changed committed reward")
			}
			if sqliteCount(t, db, "ledger") != 2 {
				t.Fatal("reward transaction audit changed")
			}
			frames = architectureCall(t, l, w, payload)
			architectureReply(t, frames, 1, 0)
			if len(frames) != 1 || sqliteCount(t, db, "ledger") != 2 {
				t.Fatal("retry paid or pushed twice")
			}
		})
	}
}

func TestArchitectureSearchReplyFailureReleasesReservation(t *testing.T) {
	for _, fail := range []int{0, 1} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			l, w, _ := architectureBaseline(t)
			s := testCTRSearch(t, l.pid(), 0)
			l.lobbyDoc = s.conn.(*lobbyConn).lobbyDoc
			request := demonware.NewWriter([]byte{145, 3, 18})
			raw, _ := json.Marshal(s.doc)
			request.String(string(raw))
			w.fail = fail
			frames := architectureCall(t, l, w, request.Bytes())
			p := architectureReply(t, frames, 18, 0)
			if len(frames) != 1 || len(testMatchmaker.pending) != 0 {
				t.Fatal("reservation callback did not run exactly once")
			}
			r := demonware.NewReader(p[26:])
			idJSON, err := r.String()
			var id struct {
				ID uint64 `json:"mm_id"`
			}
			if err != nil || json.Unmarshal([]byte(idJSON), &id) != nil || id.ID != 2 {
				t.Fatal("search counter increment moved")
			}
			if fail == 1 {
				if len(testMatchmaker.waiting) != 0 {
					t.Fatal("failed search reply retained seats")
				}
				return
			}
			if len(testMatchmaker.waiting) != 1 || testMatchmaker.waiting[0].id != id.ID {
				t.Fatal("successful search not activated")
			}
			cancel := demonware.NewWriter([]byte{145, 3, 8})
			cancel.Uint64(id.ID)
			p = architectureReply(t, architectureCall(t, l, w, cancel.Bytes()), 8, 0)
			if !bytes.Equal(p[26:], []byte{1, 1}) || len(testMatchmaker.waiting) != 0 {
				t.Fatal("search cancellation changed")
			}
		})
	}
}

func TestArchitectureContextRegistryLifecycle(t *testing.T) {
	for _, service := range []byte{21, 138} {
		t.Run(fmt.Sprint(service), func(t *testing.T) {
			l, w, _ := architectureBaseline(t)
			now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			l.clock = func() time.Time { return now }
			l.entropy = bytes.NewReader(bytes.Repeat([]byte{0x31}, 128))
			request := func(task byte) *demonware.Writer {
				q := demonware.NewWriter([]byte{service, 3, task})
				if service == 138 {
					q.String("fixture")
				}
				return q
			}
			info := func(q *demonware.Writer) {
				q.Blob(make([]byte, 67))
				q.Uint32(1)
				q.Uint32(8)
				if service == 138 {
					q.Blob([]byte{8, 9, 0, 0, 0})
				} else {
					q.Raw([]byte{8, 9, 0, 0, 0, 0}) // request terminator, removed by RequestTail
				}
			}
			p := architectureReply(t, architectureCall(t, l, w, request(19).Bytes()), 19, 0)
			r := demonware.NewReader(p[26:])
			sid, err := r.Blob()
			if err != nil || !bytes.Equal(sid, bytes.Repeat([]byte{0x31}, 8)) {
				t.Fatal("reservation entropy not scoped")
			}
			var key [8]byte
			copy(key[:], sid)
			for _, task := range []byte{20, 2, 12} {
				q := request(task)
				q.Blob(sid)
				if task == 12 {
					q.Uint32(2)
				}
				info(q)
				architectureReply(t, architectureCall(t, l, w, q.Bytes()), task, 0)
				s := testRegistry.sessions[key]
				if s == nil || s.reserved || s.owner != l.n || s.ownerPID != l.pid() || s.updated != now || !bytes.Equal(s.attrs, []byte{8, 9, 0, 0, 0}) {
					t.Fatal("registry state changed")
				}
			}
			if testRegistry.sessions[key].numPlayers != 2 {
				t.Fatal("player count update changed")
			}
			q := request(3)
			q.Blob(sid)
			architectureReply(t, architectureCall(t, l, w, q.Bytes()), 3, 0)
			if len(testRegistry.sessions) != 0 {
				t.Fatal("session not removed")
			}
			q = request(1)
			info(q)
			architectureReply(t, architectureCall(t, l, w, q.Bytes()), 1, 0)
			if len(testRegistry.sessions) != 1 {
				t.Fatal("session not created")
			}
			// Find through a second connection with a distinct publisher identity.
			guest, _ := sharedPS4Lobby()
			guest.n++
			guest.player.PID = 18
			guest.c = &architectureWire{}
			gw := guest.c.(*architectureWire)
			if service == 138 {
				q = request(14)
				q.Uint64(l.pid())
			} else {
				q = request(5)
				q.Uint32(0)
				q.Uint32(0)
				q.Uint32(10)
			}
			p = architectureReply(t, architectureCall(t, guest, gw, q.Bytes()), q.Bytes()[2], 0)
			if binary.LittleEndian.Uint32(p[17:21]) != 1 {
				t.Fatal("session not discoverable")
			}
		})
	}
}

func TestArchitectureStatsWritesAndReadsThroughRouter(t *testing.T) {
	l, w, db := architectureBaseline(t)
	q := demonware.NewWriter([]byte{4, 3, 1})
	q.Uint32(1001)
	q.Uint64(l.pid())
	q.Uint8(4)
	q.Int64(4000000000)
	q.Uint32(123)
	architectureReply(t, architectureCall(t, l, w, q.Bytes()), 1, 0)
	if sqliteCount(t, db, "leaderboard") != 1 {
		t.Fatal("stats write not committed")
	}
	for _, task := range []byte{4, 13} {
		q = demonware.NewWriter([]byte{4, 3, task})
		q.Uint32(1001)
		if task == 4 {
			q.Uint64(1)
			q.Uint32(1)
		} else {
			q.Uint32(1)
			q.Uint64(l.pid())
		}
		q.Uint32(0)
		q.Raw([]byte{0})
		p := architectureReply(t, architectureCall(t, l, w, q.Bytes()), task, 0)
		r := demonware.NewReader(p[26:])
		pid, _ := r.Uint64()
		score, _ := r.Int64()
		rank, _ := r.Uint64()
		name, _ := r.String()
		stamp, _ := r.Uint32()
		driver, err := r.Uint32()
		var stored uint32
		_ = db.db.QueryRow("SELECT updated FROM leaderboard").Scan(&stored)
		if err != nil || pid != l.pid() || score != 4000000000 || rank != 1 || name != l.player.Username || stamp != stored || driver != 123 || len(r.Remaining()) != 0 {
			t.Fatal("stats persisted result changed")
		}
	}
}

func TestArchitecturePairReplyPushAndDocuments(t *testing.T) {
	a, aw, _ := architectureBaseline(t)
	b, _ := sharedPS4Lobby()
	b.n++
	b.player.PID = 18
	bw := &architectureWire{}
	b.c = bw
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	oldClock := testMatchmaker.clock
	testMatchmaker.clock = func() time.Time { return now }
	t.Cleanup(func() { testMatchmaker.clock = oldClock })
	search := func(l *lobbyConn, w *architectureWire) {
		s := testCTRSearch(t, l.pid(), 0)
		q := demonware.NewWriter([]byte{145, 3, 2})
		q.String(s.conn.(*lobbyConn).lobbyDoc)
		architectureReply(t, architectureCall(t, l, w, q.Bytes()), 2, 0)
		q = demonware.NewWriter([]byte{145, 3, 18})
		raw, _ := json.Marshal(s.doc)
		q.String(string(raw))
		architectureReply(t, architectureCall(t, l, w, q.Bytes()), 18, 0)
	}
	search(a, aw)
	aw.writes = nil
	aw.Reset()
	beforeA := a.s2cSeq
	search(b, bw)
	frames := architectureFrames(t, a, aw, beforeA)
	if len(frames) != 1 || frames[0].Inner != demonware.InnerPush {
		t.Fatal("host create push missing")
	}
	p, _ := hex.DecodeString(frames[0].Payload)
	r := demonware.NewReader(p)
	event, _ := r.Uint32()
	searchID, _ := r.Uint64()
	roomID, _ := r.Uint64()
	version, _ := r.Uint64()
	host, _ := r.String()
	backend, err := r.String()
	if err != nil || event != 2005 || searchID != 2 || roomID != 5 || version != 1 || len(r.Remaining()) != 0 {
		t.Fatal("host push envelope changed")
	}
	var doc map[string]any
	if json.Unmarshal([]byte(backend), &doc) != nil || doc["create_time"] != float64(now.Unix()) || doc["update_time"] != float64(now.Unix()) {
		t.Fatal("room clock not preserved in push")
	}
	bw.writes = nil
	bw.Reset()
	beforeB := b.s2cSeq
	q := demonware.NewWriter([]byte{145, 3, 15})
	q.Uint64(roomID)
	q.Uint64(version)
	q.String(host)
	frames = architectureCall(t, a, aw, q.Bytes())
	p = architectureReply(t, frames, 15, 0)
	if len(frames) != 1 || !bytes.Equal(p[26:], []byte{1, 1}) {
		t.Fatal("sync reply changed")
	}
	frames = architectureFrames(t, b, bw, beforeB)
	if len(frames) != 1 || frames[0].Inner != demonware.InnerPush {
		t.Fatal("guest join push missing")
	}
	p, _ = hex.DecodeString(frames[0].Payload)
	r = demonware.NewReader(p)
	event, _ = r.Uint32()
	if event != 2001 {
		t.Fatal("guest push event changed")
	}
	q = demonware.NewWriter([]byte{145, 3, 13})
	q.Uint64(roomID)
	p = architectureReply(t, architectureCall(t, a, aw, q.Bytes()), 13, 0)
	r = demonware.NewReader(p[26:])
	gotHost, _ := r.String()
	gotBackend, err := r.String()
	if err != nil || gotHost != host || gotBackend != backend || len(r.Remaining()) != 0 {
		t.Fatal("documents differ from the acknowledged room")
	}
	q = demonware.NewWriter([]byte{145, 3, 10})
	q.Uint64(roomID)
	aw.fail = 1
	architectureReply(t, architectureCall(t, a, aw, q.Bytes()), 10, 0)
	if len(testMatchmaker.rooms) != 0 {
		t.Fatal("failed disband reply retained the room")
	}
}

func TestArchitectureSharedLookupAndUserData(t *testing.T) {
	l, w, db := architectureBaseline(t)
	if err := db.rememberName(18, "known", time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	q := demonware.NewWriter([]byte{12, 3, 10})
	q.Uint32(2)
	q.String(l.player.Username)
	q.String("known")
	p := architectureReply(t, architectureCall(t, l, w, q.Bytes()), 10, 0)
	if binary.LittleEndian.Uint32(p[17:21]) != 2 {
		t.Fatal("online/persisted lookup changed")
	}
	q = demonware.NewWriter([]byte{12, 3, 9})
	q.Uint64(l.pid())
	p = architectureReply(t, architectureCall(t, l, w, q.Bytes()), 9, 0)
	r := demonware.NewReader(p[26:])
	pid, _ := r.Uint64()
	name, err := r.String()
	if err != nil || pid != l.pid() || name != l.player.Username {
		t.Fatal("online name lookup changed")
	}
	q = demonware.NewWriter([]byte{29, 3, 1})
	q.String("fixture")
	q.Blob([]byte{1, 2, 3})
	architectureReply(t, architectureCall(t, l, w, q.Bytes()), 1, 0)
	q = demonware.NewWriter([]byte{29, 3, 4})
	q.String("fixture")
	q.Bool(true)
	q.Uint64(l.pid())
	p = architectureReply(t, architectureCall(t, l, w, q.Bytes()), 4, 0)
	r = demonware.NewReader(p[26:])
	pid, _ = r.Uint64()
	data, err := r.Blob()
	if err != nil || pid != l.pid() || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatal("user data result changed")
	}
	architectureReply(t, architectureCall(t, l, w, append([]byte{68, 3, 3}, rpSetRequest("fixture", l.pid(), "psn", 1, []byte{7})...)), 3, 0)
	for _, task := range []byte{4, 5} {
		p = architectureReply(t, architectureCall(t, l, w, append([]byte{68, 3, task}, rpGetRequest("fixture", l.pid())...)), task, 0)
		rows, ids := rpRows(t, p)
		if len(rows) != 1 || ids[0] != l.pid() || rows[0].platform != "psn" || rows[0].flag != 1 || !bytes.Equal(rows[0].data, []byte{7}) {
			t.Fatal("presence result changed")
		}
	}
	q = demonware.NewWriter([]byte{10, 3, 21})
	q.String("fixture")
	q.String("missing-fixture")
	p = architectureReply(t, architectureCall(t, l, w, q.Bytes()), 21, 0)
	if len(p) != 21 {
		t.Fatal("missing publisher file is not empty success")
	}
}

func TestArchitectureResultCallbackRunsOnceAfterSendAttempt(t *testing.T) {
	for _, fail := range []int{0, 1} {
		t.Run(fmt.Sprintf("fail-%d", fail), func(t *testing.T) {
			l, _ := sharedPS4Lobby()
			w := &architectureWire{fail: fail}
			l.c = w
			calls := 0
			l.deliverResult(taskResult{Reply: l.taskReply(1, 0, nil), AfterReply: func(err error) {
				calls++
				if len(w.writes) != 1 || (err != nil) != (fail == 1) {
					t.Fatal("callback ran before send or received another send outcome")
				}
			}})
			if calls != 1 || l.afterReplyActive {
				t.Fatal("callback was not completed exactly once")
			}
			architectureFrames(t, l, w, 0)
		})
	}
}
