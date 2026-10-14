// These fixtures were recorded against 33dc1c6 before changing application
// code. Only synthetic requests are recorded. Random ciphertext is never a
// fixture: every attempted frame is authenticated and decrypted first.

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

type architectureFrame struct {
	Sequence  uint32
	Inner     byte
	Payload   string
	Delivered bool
}

type architectureWire struct {
	platformWire
	writes [][]byte
	fail   int
}

func (w *architectureWire) Write(p []byte) (int, error) {
	w.writes = append(w.writes, bytes.Clone(p))
	if w.fail == len(w.writes) {
		return 0, errors.New("synthetic write failure")
	}
	return w.platformWire.Write(p)
}

func architectureFrames(t *testing.T, l *lobbyConn, w *architectureWire, before uint32) []architectureFrame {
	t.Helper()
	var out []architectureFrame
	for i, raw := range w.writes {
		if len(raw) < 4 || int(binary.LittleEndian.Uint32(raw))+4 != len(raw) {
			t.Fatal("invalid outer frame size")
		}
		frame, err := demonware.ParseEncryptedFrame(raw)
		if err != nil || !frame.Verify(l.Keys.S2CMAC) {
			t.Fatal("invalid server frame or MAC")
		}
		if frame.Sequence != before+uint32(i)+1 {
			t.Fatal("server sequence changed")
		}
		message, err := frame.Decrypt(l.Keys.S2CAES)
		if err != nil {
			t.Fatal("invalid server plaintext")
		}
		out = append(out, architectureFrame{frame.Sequence, message.Type, hex.EncodeToString(message.Payload), w.fail != i+1})
	}
	if l.S2CSeq != before+uint32(len(w.writes)) {
		t.Fatal("write failure changed sequence increment point")
	}
	return out
}

type architectureSnapshot struct {
	Frames       []architectureFrame
	Transactions uint64
	Sessions     int
	UserData     int
	RichPresence int
	Waiting      int
	Pending      int
	Rooms        int
	LobbyDoc     string
	Accounts     int
	Inventory    int
	Ledger       int
	AfterReply   bool
	SendFailed   bool
}

// Deliberately serial: the baseline uses process globals. Cleanup restores all
// replaced registries; the instance refactor must retain these observations.
func architectureBaseline(t *testing.T) (*lobbyConn, *architectureWire, *store.SQLite) {
	t.Helper()
	oldSessions, oldPresence := testRegistry.Sessions, testPresence
	oldEconomy, oldStats, oldNews := testCTR.Economy, testCTR.Stats, testCTR.News
	oldWaiting, oldPending, oldRooms := testMatchmaker.Waiting, testMatchmaker.Pending, testMatchmaker.Rooms
	oldTx, oldIDs := testReplies.Transactions.Load(), testMatchmaker.IDs.Load()
	testRegistry.Sessions = map[[8]byte]*lobby.MatchSession{}
	testMatchmaker.Waiting, testMatchmaker.Pending, testMatchmaker.Rooms = nil, nil, map[uint64]*ctr.Room{}
	testReplies.Transactions.Store(0)
	testMatchmaker.IDs.Store(0)
	e, db := newTestEconomy(t, false)
	testCTR.Economy, testCTR.Stats, testCTR.News = e, db, config.News{}
	testPresence = presence.NewStore(db)
	setStartBalance(t, 10000)
	l, _ := sharedPS4Lobby()
	w := &architectureWire{}
	l.C = w
	l.pubDir = t.TempDir()
	testPresence.RegisterOnline(l.N, l.Player)
	t.Cleanup(func() {
		testRegistry.Sessions, testPresence = oldSessions, oldPresence
		testCTR.Economy, testCTR.Stats, testCTR.News = oldEconomy, oldStats, oldNews
		testMatchmaker.Waiting, testMatchmaker.Pending, testMatchmaker.Rooms = oldWaiting, oldPending, oldRooms
		testReplies.Transactions.Store(oldTx)
		testMatchmaker.IDs.Store(oldIDs)
	})
	return l, w, db
}

func architectureState(t *testing.T, l *lobbyConn, w *architectureWire, db *store.SQLite, request []byte, before, after time.Time) architectureSnapshot {
	t.Helper()
	frames := architectureFrames(t, l, w, 0)
	// The baseline has no clock seam. Only getServerTime's single result may
	// vary: validate its actual value within the call interval before replacing
	// precisely those four bytes. Reward/event timestamps are never normalized.
	if len(request) >= 3 && bytes.Equal(request[:3], []byte{12, 3, 6}) {
		for i := range frames {
			p, _ := hex.DecodeString(frames[i].Payload)
			if len(p) != 31 || p[26] != demonware.TagUint32 {
				t.Fatal("server time shape changed")
			}
			now := int64(binary.LittleEndian.Uint32(p[27:31]))
			if now < before.Unix() || now > after.Unix() {
				t.Fatal("server time outside call interval")
			}
			clear(p[27:31])
			frames[i].Payload = hex.EncodeToString(p)
		}
	}
	// A reservation returns the same random eight-byte ID used by the registry.
	// Verify that relationship before normalizing this one entropy field.
	if len(request) >= 3 && request[1] == 3 && request[2] == 19 && (request[0] == 21 || request[0] == 138) && len(testRegistry.Sessions) == 1 {
		for i := range frames {
			p, _ := hex.DecodeString(frames[i].Payload)
			if len(p) != 40 {
				t.Fatal("reservation reply shape changed")
			}
			var id [8]byte
			copy(id[:], p[32:40])
			s := testRegistry.Sessions[id]
			if s == nil || !s.Reserved || s.Owner != l.N || s.OwnerPID != l.PID() || s.Updated.Before(before) || s.Updated.After(after) {
				t.Fatal("reservation state does not match reply")
			}
			clear(p[32:40])
			frames[i].Payload = hex.EncodeToString(p)
		}
	}
	return architectureSnapshot{frames, testReplies.Transactions.Load(), len(testRegistry.Sessions), testPresence.Counts().UserData, testPresence.Counts().RichPresence, len(testMatchmaker.Waiting), len(testMatchmaker.Pending), len(testMatchmaker.Rooms), l.lobbyDoc,
		sqliteCount(t, db, "accounts"), sqliteCount(t, db, "inventory"), sqliteCount(t, db, "ledger"), l.AfterReplyActive, l.ReplyErr != nil}
}

func TestArchitectureDispatcherBaseline(t *testing.T) {
	// Includes every handled selector, service defaults and the global default.
	pairs := map[byte][]byte{
		4: {1, 4, 13, 255}, 10: {21, 255}, 12: {6, 9, 10, 255}, 21: {1, 2, 3, 5, 12, 19, 20, 255},
		29: {1, 4, 255}, 38: {6, 255}, 68: {3, 4, 5, 7, 255}, 80: {154, 165, 245, 246, 255},
		104: {4, 5, 255}, 125: {1, 2, 3, 9, 255}, 138: {1, 2, 3, 12, 14, 19, 20, 255},
		145: {2, 3, 4, 5, 6, 7, 8, 10, 13, 14, 15, 17, 18, 255}, 194: {1, 255}, 255: {255},
	}
	cases := map[string][]byte{"empty": nil, "short-service": {4}, "short-tag": {4, 3}, "raw-bandwidth": {18, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	for service, tasks := range pairs {
		for _, task := range tasks {
			prefix := fmt.Sprintf("%03d-%03d", service, task)
			cases[prefix+"-empty"] = []byte{service, 3, task}
			cases[prefix+"-malformed"] = []byte{service, 3, task, 0xff}
			cases[prefix+"-unreadable-task"] = []byte{service, 1, task}
		}
	}
	cases["marketplace-purchase"] = marketplaceHex(t, ps4PurchaseRequest)
	cases["marketplace-balances"] = marketplaceHex(t, ps4BalancesRequest)
	cases["news-fetch"] = newsFetch("fixture", "en", [2]uint32{1, 1})
	cases["news-viewed"] = newsViewed("fixture", "en", 1)
	for _, task := range []byte{1, 2, 3, 12, 14, 19, 20, 255} {
		w := demonware.NewWriter([]byte{138, 3, task})
		w.String("fixture")
		cases[fmt.Sprintf("context-%03d", task)] = w.Bytes()
	}
	for _, service := range []byte{29, 68} {
		w := demonware.NewWriter([]byte{service, 3, 1})
		w.String("fixture")
		if service == 68 {
			w = demonware.NewWriter([]byte{68, 3, 3})
			w.String("fixture")
			w.Uint64(0)
			w.String("psn")
			w.Uint8(1)
		}
		w.Blob([]byte{1, 2, 3})
		cases[fmt.Sprintf("state-%03d", service)] = w.Bytes()
	}
	const path = "testdata/architecture_dispatch_baseline.json"
	var expected map[string]architectureSnapshot
	if os.Getenv("UPDATE_ARCHITECTURE_FIXTURES") != "1" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &expected); err != nil {
			t.Fatal(err)
		}
	}
	actual := map[string]architectureSnapshot{}
	for name, payload := range cases {
		for _, fail := range []int{0, 1, 2} {
			name := fmt.Sprintf("%s/write-fail-%d", name, fail)
			t.Run(name, func(t *testing.T) {
				l, w, db := architectureBaseline(t)
				if len(payload) >= 3 && payload[0] == 125 && payload[1] == 3 && payload[2] == 3 {
					// Recorded on 2026-10-14 before the 15:00 UTC period boundary.
					// An empty structured request selects active achievements too.
					// Keep their exact recorded dates; do not normalize the reply.
					l.Clock = func() time.Time { return time.Date(2026, 10, 14, 14, 0, 0, 0, time.UTC) }
				}
				w.fail = fail
				before := time.Now()
				l.onTask(payload)
				after := time.Now()
				got := architectureState(t, l, w, db, payload, before, after)
				actual[name] = got
				want := expected[name]
				if expected != nil {
					want = architectureDiabloDisconnection(name, want)
				}
				if expected != nil && !reflect.DeepEqual(got, want) {
					t.Error("baseline frames/state changed; inspect the fixture, do not regenerate it")
				}
			})
		}
	}
	if expected != nil && len(actual) != len(expected) {
		t.Fatal("baseline case set changed")
	}
	if os.Getenv("UPDATE_ARCHITECTURE_FIXTURES") == "1" && !t.Failed() {
		raw, err := json.MarshalIndent(actual, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
