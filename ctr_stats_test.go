package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"testing"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

func TestCTRStatsPersistenceBestAndWire(t *testing.T) {
	s := &ctrStatsStore{dir: t.TempDir()}
	row := ctrStatWrite{ctrStat: ctrStat{Board: 1001, PID: 12, Score: 4000000000, Driver: 123, Name: "Racer", Updated: 100}, Operation: 4}
	if err := s.write([]ctrStatWrite{row}); err != nil {
		t.Fatal(err)
	}
	row.Score--
	row.Driver = 999
	if err := s.write([]ctrStatWrite{row}); err != nil {
		t.Fatal(err)
	}
	other := row
	other.PID = 13
	other.Score = 4100000000
	if err := s.write([]ctrStatWrite{other}); err != nil {
		t.Fatal(err)
	}
	rows, err := (&ctrStatsStore{dir: s.dir}).read(1001)
	if err != nil || len(rows) != 2 || rows[0].PID != 13 || rows[1].Driver != 123 || rows[1].Rank != 2 {
		t.Fatalf("bad persistent ranks: %+v %v", rows, err)
	}
	reply := statsReply(4, rows[1:], 2)
	r := demonware.NewReader(reply)
	r.Uint64()
	r.Uint32()
	r.Uint8()
	n, _ := r.Uint32()
	total, _ := r.Uint32()
	pid, _ := r.Uint64()
	score, _ := r.Int64()
	rank, _ := r.Uint64()
	name, _ := r.String()
	stamp, _ := r.Uint32()
	driver, e := r.Uint32()
	if n != 1 || total != 2 || pid != 12 || score != 4000000000 || rank != 2 || name != "Racer" || stamp != 100 || driver != 123 || e != nil || r.Position() != len(reply) {
		t.Fatalf("bad reply %x", reply)
	}
	before, _ := os.ReadFile(s.path())
	row.Score = 4200000000
	if err := s.write([]ctrStatWrite{row}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.path())
	if bytes.Equal(before, after) {
		t.Fatal("faster score not saved")
	}
	if err := os.WriteFile(s.path(), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.write([]ctrStatWrite{row}); err == nil {
		t.Fatal("corrupt data overwritten")
	}
}
func TestCTRStatsCapturedInitializationAndOwnership(t *testing.T) {
	// Actual 4/1 initialization from the user's Highscores attempt, including task terminator.
	packet, _ := hex.DecodeString("04030108e80300000a4edf496b00000000030009000000000000000000")
	writes, err := parseStatWrites(demonware.NewReader(packet[3:]), 1800003406, "Collecting")
	if err != nil || len(writes) != 1 || writes[0].Board != 1000 {
		t.Fatalf("capture failed: %+v %v", writes, err)
	}
	if _, err = parseStatWrites(demonware.NewReader(packet[3:]), 7, "forged"); err == nil {
		t.Fatal("accepted other account")
	}
	w := &demonware.Writer{}
	w.Uint32(1001)
	w.Uint64(12)
	w.Uint8(4)
	w.Int64(4000000000)
	w.Uint32(123)
	if rows, err := parseStatWrites(demonware.NewReader(w.Bytes()), 12, "test"); err != nil || rows[0].Driver != 123 {
		t.Fatal(rows, err)
	}
	if _, err := parseStatWrites(demonware.NewReader(w.Bytes()[:len(w.Bytes())-1]), 12, "test"); err == nil {
		t.Fatal("accepted truncated driver")
	}
}
func TestCTRStatsRankAndFriendsRequests(t *testing.T) {
	oldOnline, oldStore := online, ctrStats
	defer func() { online = oldOnline; ctrStats = oldStore }()
	online = map[uint64]*playerID{9: {PID: 12, Username: "test", Title: 5760}}
	ctrStats = &ctrStatsStore{dir: t.TempDir()}
	l := &lobbyConn{n: 9, player: online[9]}
	if err := ctrStats.write([]ctrStatWrite{{ctrStat: ctrStat{Board: 1001, PID: 12, Score: 400, Name: "A"}, Operation: 4}, {ctrStat: ctrStat{Board: 1001, PID: 13, Score: 500, Name: "B"}, Operation: 4}}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []byte{4, 13} {
		w := &demonware.Writer{}
		w.Uint32(1001)
		if task == 4 {
			w.Uint64(2)
			w.Uint32(1)
		} else {
			w.Uint32(1)
			w.Uint64(12)
		}
		w.Uint32(0)
		w.Raw([]byte{0})
		r := demonware.NewReader(l.onCTRStats(task, demonware.NewReader(w.Bytes())))
		r.Uint64()
		code, _ := r.Uint32()
		r.Uint8()
		n, _ := r.Uint32()
		r.Uint32()
		pid, _ := r.Uint64()
		if code != 0 || n != 1 || pid != 12 {
			t.Fatalf("task %d wrong page/filter", task)
		}
	}
}

func TestRingRallyScoresAndDriverWire(t *testing.T) {
	for _, board := range []uint32{89, 99, 101, 133, 1002} {
		w := &demonware.Writer{}
		w.Uint32(board)
		w.Uint64(12)
		w.Uint8(4)
		w.Int64(123456)
		w.Uint32(77)
		// A generic counter after the extended record must remain a separate write.
		w.Uint32(150)
		w.Uint64(12)
		w.Uint8(1)
		w.Int64(1)
		w.Raw([]byte{0})
		writes, err := parseStatWrites(demonware.NewReader(w.Bytes()), 12, "Racer")
		if err != nil || len(writes) != 2 || writes[0].Driver != 77 || writes[1].Board != 150 {
			t.Fatalf("board %d: %+v %v", board, writes, err)
		}
		s := &ctrStatsStore{dir: t.TempDir()}
		if err = s.write(writes); err != nil {
			t.Fatal(err)
		}
		writes[0].Score--
		writes[0].Driver = 88
		if err = s.write(writes[:1]); err != nil {
			t.Fatal(err)
		}
		rows, err := (&ctrStatsStore{dir: s.dir}).read(board)
		if err != nil || len(rows) != 1 || rows[0].Score != 123456 || rows[0].Driver != 77 {
			t.Fatalf("bad saved score: %+v %v", rows, err)
		}
		r := demonware.NewReader(statsReply(4, rows, 1))
		r.Uint64()
		r.Uint32()
		r.Uint8()
		r.Uint32()
		r.Uint32()
		r.Uint64()
		r.Int64()
		r.Uint64()
		r.String()
		r.Uint32()
		driver, err := r.Uint32()
		if err != nil || driver != 77 || len(r.Remaining()) != 0 {
			t.Fatalf("missing reply driver board %d", board)
		}
	}
	for _, board := range []uint32{88, 100, 134, 150, 1000} {
		if ringBoard(board) {
			t.Fatalf("incorrect ring board %d", board)
		}
	}
}
