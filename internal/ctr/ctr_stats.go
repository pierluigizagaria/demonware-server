// bdStats service 4. Wire layouts recovered from CTR 1.0.15's bdStatsInfo
// and COctaneLeaderboardStatsInfo serializers, not from the dashboard API.

package ctr

import (
	json "encoding/json"
	fmt "fmt"
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	math "math"
	os "os"
	filepath "path/filepath"
	sort "sort"
	sync "sync"
	time "time"
)

type CtrStat struct {
	Board   uint32
	PID     uint64
	Score   int64
	Driver  uint32
	Name    string
	Updated uint32
	Rank    uint64 `json:"-"`
}

type CtrStatWrite struct {
	CtrStat
	Operation byte
}

type CtrStatsStore struct {
	FallbackDir func() string
	mu          sync.Mutex
	Dir         string
}

func timeBoard(board uint32) bool { return board > 1000 && board < 1159 }

// Ring Challenge enum IDs from CTR 1.0.15. ID 100 is not a track board.
// Ring Rally uses raw points, but shares the Octane driver extension with times.
func RingBoard(board uint32) bool { return board >= 89 && board <= 133 && board != 100 }

func octaneBoard(board uint32) bool { return timeBoard(board) || RingBoard(board) }

func statsEnd(r *demonwarepkg.Reader) bool {
	return len(r.Remaining()) == 0 || (len(r.Remaining()) == 1 && r.Remaining()[0] == 0)
}

func ParseStatWrites(r *demonwarepkg.Reader, pid uint64, name string) ([]CtrStatWrite, error) {
	out := []CtrStatWrite{}
	for !statsEnd(r) {
		if len(out) >= 128 {
			return nil, fmt.Errorf("too many writes")
		}
		board, e1 := r.Uint32()
		entity, e2 := r.Uint64()
		op, e3 := r.Uint8()
		score, e4 := r.Int64()
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || entity != pid || pid == 0 {
			return nil, fmt.Errorf("invalid stats write or owner")
		}
		if op != 0 && op != 1 && op != 4 {
			return nil, fmt.Errorf("unsupported write operation %d", op)
		}
		row := CtrStatWrite{CtrStat: CtrStat{Board: board, PID: pid, Score: score, Name: name, Updated: uint32(time.Now().Unix())}, Operation: op}
		if octaneBoard(board) {
			var err error
			row.Driver, err = r.Uint32()
			if err != nil {
				return nil, err
			}
			if op != 4 || score < 0 || (timeBoard(board) && (score == 0 || score > math.MaxUint32)) {
				return nil, fmt.Errorf("invalid Octane leaderboard record for board %d", board)
			}
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty stats write")
	}
	return out, nil
}

func (s *CtrStatsStore) Path() string {
	d := s.Dir
	if d == "" && s.FallbackDir != nil {
		d = s.FallbackDir()
	}
	return filepath.Join(d, "ctr-leaderboards-v1.json")
}

func (s *CtrStatsStore) Load() (map[uint32]map[uint64]CtrStat, error) {
	rows := map[uint32]map[uint64]CtrStat{}
	b, err := os.ReadFile(s.Path())
	if os.IsNotExist(err) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, fmt.Errorf("null leaderboard store")
	}
	return rows, nil
}

func (s *CtrStatsStore) Write(writes []CtrStatWrite) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.Load()
	if err != nil {
		return err
	}
	for _, w := range writes {
		board := rows[w.Board]
		if board == nil {
			board = map[uint64]CtrStat{}
			rows[w.Board] = board
		}
		old, exists := board[w.PID]
		next := w.CtrStat
		switch w.Operation {
		case 1:
			if (w.Score > 0 && old.Score > math.MaxInt64-w.Score) || (w.Score < 0 && old.Score < math.MinInt64-w.Score) {
				return fmt.Errorf("score overflow")
			}
			next.Score += old.Score
		case 4:
			// CTR encodes elapsed time into a descending uint32 score: faster is larger.
			if exists && old.Score >= w.Score {
				continue
			}
		}
		board[w.PID] = next
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Path()), ".ctr-stats-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, s.Path())
}

func (s *CtrStatsStore) Read(board uint32) ([]CtrStat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.Load()
	if err != nil {
		return nil, err
	}
	rows := []CtrStat{}
	for _, v := range all[board] {
		rows = append(rows, v)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].PID < rows[j].PID
		}
		return rows[i].Score > rows[j].Score
	})
	for i := range rows {
		rows[i].Rank = uint64(i + 1)
	}
	return rows, nil
}

func StatsReply(replies *lobbypkg.ReplyCounter, task byte, rows []CtrStat, total uint32) []byte {
	w := &demonwarepkg.Writer{}
	for _, r := range rows {
		w.Uint64(r.PID)
		w.Int64(r.Score)
		w.Uint64(r.Rank)
		name := r.Name
		if len(name) > 63 {
			name = name[:63]
		}
		w.String(name)
		w.Uint32(r.Updated)
		if octaneBoard(r.Board) {
			w.Uint32(r.Driver)
		}
	}
	return demonwarepkg.ListReply(replies.NextTransaction(), task, 0, uint32(len(rows)), total, w.Bytes())
}

func (h *StatsHandler) OnCTRStats(peer lobbypkg.TaskSession, task byte, r *demonwarepkg.Reader) []byte {
	pid := peer.PID()
	p := h.Presence.ConnectionPlayer(peer.ConnID())
	name := ""
	allowed := p != nil && ps4pkg.IsCTRTitle(p.Title) && pid != 0
	if allowed {
		name = p.Username
	}
	fail := func(err error) []byte {
		peer.Logf("CTR stats task=%d failed: %v", task, err)
		return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
	}
	if !allowed {
		return fail(fmt.Errorf("CTR identity required"))
	}
	if task == 1 {
		writes, err := ParseStatWrites(r, pid, name)
		if err != nil {
			return fail(err)
		}
		if err = h.Stats.Write(writes); err != nil {
			return fail(err)
		}
		peer.Logf("CTR stats saved pid=%d records=%d", pid, len(writes))
		for _, row := range writes {
			peer.Logf("CTR stats upload board=%d score=%d operation=%d", row.Board, row.Score, row.Operation)
		}
		return h.TaskReply(task, 0, nil)
	}
	if task != 4 && task != 13 {
		return fail(fmt.Errorf("unsupported stats task"))
	}
	board, err := r.Uint32()
	if err != nil {
		return fail(err)
	}
	var start uint64
	var limit uint32
	ids := map[uint64]bool{}
	if task == 4 {
		start, err = r.Uint64()
		if err != nil {
			return fail(err)
		}
		limit, err = r.Uint32()
		if err != nil || limit > 1000 {
			return fail(fmt.Errorf("invalid rank limit"))
		}
		if start == 0 {
			start = 1
		}
	} else {
		n, e := r.Uint32()
		if e != nil || n > 1000 {
			return fail(fmt.Errorf("invalid entity count"))
		}
		for i := uint32(0); i < n; i++ {
			id, e := r.Uint64()
			if e != nil {
				return fail(e)
			}
			ids[id] = true
		}
	}
	cols, e := r.Uint32()
	if e != nil || cols != 0 || !statsEnd(r) {
		return fail(fmt.Errorf("unsupported column selection"))
	}
	rows, total, err := ReadLeaderboard(h.Stats, task, board, start, limit, ids)
	if err != nil {
		return fail(err)
	}
	peer.Logf("CTR stats read task=%d board=%d rows=%d total=%d", task, board, len(rows), total)
	return StatsReply(h.ReplyCounter, task, rows, total)
}
