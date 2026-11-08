package storage

import (
	"context"
	"encoding/json"
	"strings"
)

// Feed previews use a bounded stored representation; legacy rows are filled lazily.
func (s *Store) ListActivityRoutePreviewPoints(ctx context.Context, ids []int64, maxPoints int) (map[int64][]ActivityRoutePoint, error) {
	if maxPoints > 48 {
		return s.loadActivityRoutePreviewPoints(ctx, ids, maxPoints)
	}
	result := make(map[int64][]ActivityRoutePoint, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT activity_id,points FROM activity_route_previews WHERE activity_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var points []ActivityRoutePoint
		if err = json.Unmarshal([]byte(raw), &points); err != nil {
			rows.Close()
			return nil, err
		}
		result[id] = sampleActivityRoutePoints(points, maxPoints)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var missing []int64
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			missing = append(missing, id)
		}
	}
	loaded, err := s.loadActivityRoutePreviewPoints(ctx, missing, 48)
	if err != nil {
		return nil, err
	}
	for id, points := range loaded {
		raw, err := json.Marshal(points)
		if err != nil {
			return nil, err
		}
		// A concurrent deletion must not recreate even a simplified route.
		if _, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO activity_route_previews(activity_id,points) SELECT ?,? WHERE EXISTS(SELECT 1 FROM activities WHERE id=?)`, id, string(raw), id); err != nil {
			return nil, err
		}
		result[id] = sampleActivityRoutePoints(points, maxPoints)
	}
	return result, nil
}
