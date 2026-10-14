package ctr

// A nil backend retains the upstream JSON reference implementation. Runtime
// installs SQLite; economyStore.mu covers each complete load/mutate/save.
type economyBackend interface {
	Load(pid uint64, create bool) (*EconomyAccount, error)
	Save(pid uint64, a *EconomyAccount, change EconomyChange) error
}

type EconomyChange struct {
	Source string
	SKUs   []uint32
}

type LeaderboardStore interface {
	Write(writes []StatWrite) error
	Read(board uint32) ([]Stat, error)
}

// rankedLeaderboard answers stats reads without loading a whole board.
type rankedLeaderboard interface {
	ReadRanks(board uint32, start uint64, limit uint32) ([]Stat, uint64, error)
	ReadEntities(board uint32, pids []uint64) ([]Stat, error)
}

// ReadLeaderboard returns the rows for stats task 4 (ranks from start) or 13
// (the given entities) and the total the reply reports.
func ReadLeaderboard(store LeaderboardStore, task byte, board uint32, start uint64, limit uint32, ids map[uint64]bool) ([]Stat, uint32, error) {
	if ranked, ok := store.(rankedLeaderboard); ok {
		if task == 4 {
			rows, total, err := ranked.ReadRanks(board, start, limit)
			return rows, uint32(total), err
		}
		pids := make([]uint64, 0, len(ids))
		for pid := range ids {
			pids = append(pids, pid)
		}
		rows, err := ranked.ReadEntities(board, pids)
		return rows, uint32(len(rows)), err
	}
	all, err := store.Read(board)
	if err != nil {
		return nil, 0, err
	}
	rows := []Stat{}
	for _, row := range all {
		if task == 4 {
			if row.Rank >= start && uint32(len(rows)) < limit {
				rows = append(rows, row)
			}
		} else if ids[row.PID] {
			rows = append(rows, row)
		}
	}
	if task == 13 {
		return rows, uint32(len(rows)), nil
	}
	return rows, uint32(len(all)), nil
}
