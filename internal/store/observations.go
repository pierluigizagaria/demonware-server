package store

// Activity is a copied observation of this connection pool's write count.
// It exposes neither the SQL handle nor a way to modify the database.
type Activity struct{ TotalChanges int }

func (s *SQLite) Activity() (Activity, error) {
	var activity Activity
	err := s.db.QueryRow("SELECT total_changes()").Scan(&activity.TotalChanges)
	return activity, err
}
