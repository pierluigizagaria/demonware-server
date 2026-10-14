package main

import (
	"encoding/binary"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

// request builders in the documented layout
func rpStr(s string) []byte { return append(append([]byte{demonware.TagString}, s...), 0) }

func rpU64(v uint64) []byte {
	return binary.LittleEndian.AppendUint64([]byte{demonware.TagUint64}, v)
}

func rpU32(v uint32) []byte {
	return binary.LittleEndian.AppendUint32([]byte{demonware.TagUint32}, v)
}

func rpBlob(b []byte) []byte {
	return append(append([]byte{demonware.TagBlob}, rpU32(uint32(len(b)))...), b...)
}

func rpSetRequest(ctx string, id uint64, provider string, flag byte, data []byte) []byte {
	out := rpStr(ctx)
	out = append(out, rpU64(id)...)
	out = append(out, rpStr(provider)...)
	out = append(out, demonware.TagUint8, flag)
	return append(out, rpBlob(data)...)
}

func rpGetRequest(ctx string, ids ...uint64) []byte {
	out := append(rpStr(ctx), rpU32(uint32(len(ids)))...)
	for _, id := range ids {
		out = append(append(out, rpU64(id)...), rpStr("nx")...)
	}
	return out
}

// parse a get reply: transaction, error, task, count, [total, rows]
func rpRows(t *testing.T, reply []byte) (rows []presence.RichPresence, ids []uint64) {
	t.Helper()
	r := demonware.NewReader(reply)
	if _, err := r.Uint64(); err != nil {
		t.Fatal(err)
	}
	if code, err := r.Uint32(); err != nil || code != 0 {
		t.Fatalf("error code %d %v", code, err)
	}
	if _, err := r.Uint8(); err != nil {
		t.Fatal(err)
	}
	n, err := r.Uint32()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return nil, nil
	}
	if _, err := r.Uint32(); err != nil { // total
		t.Fatal(err)
	}
	for i := uint32(0); i < n; i++ {
		id, e1 := r.Uint64()
		plat, e2 := r.String()
		flag, e3 := r.Uint8()
		data, e4 := r.Blob()
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			t.Fatalf("row %d unreadable", i)
		}
		rows, ids = append(rows, presence.RichPresence{Platform: plat, Flag: flag, Data: data}), append(ids, id)
	}
	return rows, ids
}

func withOnline(t *testing.T, players map[uint64]*platform.PlayerID) {
	t.Helper()
	testPresence.OnlineMu.Lock()
	old := testPresence.Online
	testPresence.Online = players
	testPresence.OnlineMu.Unlock()
	testPresence.RichMu.Lock()
	testPresence.Rich = map[uint64]presence.RichPresence{}
	testPresence.RichMu.Unlock()
	t.Cleanup(func() { testPresence.OnlineMu.Lock(); testPresence.Online = old; testPresence.OnlineMu.Unlock() })
}

func TestRichPresenceSetAndGet(t *testing.T) {
	alice := &platform.PlayerID{Username: "player1", PID: 1800000101}
	bob := &platform.PlayerID{Username: "player2", PID: 1800000102}
	withOnline(t, map[uint64]*platform.PlayerID{1: alice, 2: bob})
	la, lb := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 1, Player: alice}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR}), newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 2, Player: bob}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})

	// player1 sets a presence, as the game does (id 0 = me)
	la.onRichPresence(presence.RPSet, demonware.NewReader(rpSetRequest("", 0, "", 1, []byte("hello"))))

	// player2 asks about player1, by PID (the way Citron identifies a friend)
	rows, ids := rpRows(t, lb.onRichPresence(presence.RPGetAndSubscribe, demonware.NewReader(rpGetRequest("", alice.PID))))
	if len(rows) != 1 || string(rows[0].Data) != "hello" || rows[0].Flag != 1 || ids[0] != alice.PID {
		t.Fatalf("got %+v ids %v", rows, ids)
	}
	// Explicit offline states for someone with no published presence or who is offline.
	rows, _ = rpRows(t, lb.onRichPresence(presence.RPGet, demonware.NewReader(rpGetRequest("", bob.PID, 999))))
	if len(rows) != 2 || rows[0].Flag != 0 || rows[1].Flag != 0 || len(rows[0].Data) != 0 || len(rows[1].Data) != 0 {
		t.Fatalf("unexpected rows %+v", rows)
	}
	// id 0 means the asker
	rows, _ = rpRows(t, la.onRichPresence(presence.RPGet, demonware.NewReader(rpGetRequest("", 0))))
	if len(rows) != 1 {
		t.Fatalf("own presence: %+v", rows)
	}
}

func TestRichPresenceGoneWhenThePlayerLeaves(t *testing.T) {
	alice := &platform.PlayerID{Username: "player1", PID: 1800000101}
	bob := &platform.PlayerID{Username: "player2", PID: 1800000102}
	withOnline(t, map[uint64]*platform.PlayerID{1: alice, 2: bob})
	la, lb := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 1, Player: alice}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR}), newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 2, Player: bob}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	la.onRichPresence(presence.RPSet, demonware.NewReader(rpSetRequest("", 0, "", 1, []byte("x"))))

	testPresence.DropOnline(1)
	if rows, _ := rpRows(t, lb.onRichPresence(presence.RPGet, demonware.NewReader(rpGetRequest("", alice.PID)))); len(rows) != 1 || rows[0].Flag != 0 || len(rows[0].Data) != 0 {
		t.Fatalf("a player who left is still shown: %+v", rows)
	}
	testPresence.RichMu.Lock()
	_, kept := testPresence.Rich[alice.PID]
	testPresence.RichMu.Unlock()
	if kept {
		t.Error("presence not dropped")
	}
}

func TestRichPresenceFromTheRealLog(t *testing.T) {
	// the bytes after "service 68 task 3" from a real Diablo III login
	raw := []byte{0x10, 0x00, 0x0a, 0, 0, 0, 0, 0, 0, 0, 0, 0x10, 0x00, 0x03, 0x01, 0x13, 0x08, 0x04, 0, 0, 0, 0xde, 0xad, 0xbe, 0xef}
	alice := &platform.PlayerID{Username: "player1", PID: 1800000101}
	withOnline(t, map[uint64]*platform.PlayerID{1: alice})
	l := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 1, Player: alice}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR})
	l.onRichPresence(presence.RPSet, demonware.NewReader(raw))
	testPresence.RichMu.Lock()
	p, ok := testPresence.Rich[alice.PID]
	testPresence.RichMu.Unlock()
	if !ok || p.Flag != 1 || len(p.Data) != 4 || p.Data[0] != 0xde {
		t.Fatalf("stored %+v ok=%v", p, ok)
	}
}

func TestRichPresenceNeedsAPlayer(t *testing.T) {
	withOnline(t, map[uint64]*platform.PlayerID{})
	guest := newTestLobby(&lobby.Session{Telemetry: testTelemetry, Diagnostics: testDiagnostics, Options: testOptions, ReplyCounter: testReplies, Presence: testPresence, Tickets: testTickets, N: 9}, lobbyDependencies{files: testFiles, relay: testRelay, matchmaker: testMatchmaker, registry: testRegistry, ctr: testCTR}) // no identity
	guest.onRichPresence(presence.RPSet, demonware.NewReader(rpSetRequest("", 0, "", 1, []byte("x"))))
	testPresence.RichMu.Lock()
	n := len(testPresence.Rich)
	testPresence.RichMu.Unlock()
	if n != 0 {
		t.Errorf("a player without a PID stored a presence")
	}
	// a truncated request must not panic
	guest.onRichPresence(presence.RPSet, demonware.NewReader([]byte{demonware.TagString, 0, demonware.TagUint64, 1}))
	guest.onRichPresence(presence.RPGet, demonware.NewReader(rpU32(1<<30)))
}
