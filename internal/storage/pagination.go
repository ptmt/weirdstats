package storage

import (
	"context"
	"time"
)

type ActivityCursor struct {
	StartUnix int64 `json:"time"`
	ID        int64 `json:"id"`
}

func (s *Store) ListActivitiesPage(ctx context.Context, userID int64, limit int, start, end time.Time, cursor ActivityCursor) ([]ActivityWithStats, error) {
	return s.listActivitiesWithStats(ctx, userID, limit, start, end, cursor)
}
