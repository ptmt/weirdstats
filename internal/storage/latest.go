package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// RequestLatestSync reuses active/blocked work and briefly coalesces completed
// requests. All insertion/cooldown decisions are one SQLite transaction.
func (s *Store) RequestLatestSync(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return errors.New("owner required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	payload, _ := json.Marshal(struct {
		UserID int64 `json:"user_id"`
	}{userID})
	// INSERT..SELECT is atomic even when independent HTTP requests race.
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs(type,status,payload,cursor,attempts,max_attempts,last_error,next_run_at,created_at,updated_at,user_id,activity_id,parent_id,error_code)
 SELECT 'sync_latest','queued',?,'{}',0,10,'',?,?,?, ?,0,0,''
 WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE user_id=? AND type='sync_latest' AND (status IN ('queued','running','retry','waiting','blocked') OR updated_at>=?))`, string(payload), time.Now().Unix(), time.Now().Unix(), time.Now().Unix(), userID, userID, time.Now().Add(-time.Minute).Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Use the last fully committed scan boundary, never the newest saved activity:
// a webhook may have saved that activity while leaving an older gap.
func (s *Store) LatestSyncAfter(ctx context.Context, userID int64) (int64, error) {
	var after int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(json_extract(cursor,'$.before_unix') AS INTEGER)),0) FROM jobs WHERE user_id=? AND type='sync_latest' AND status='completed' AND json_valid(cursor)`, userID).Scan(&after)
	if err != nil || after > 0 {
		return after - 1, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(start_time),?) FROM activities WHERE user_id=?`, time.Now().Add(-7*24*time.Hour).Unix(), userID).Scan(&after)
	return after - 1, err
}

func (s *Store) HasActivityForUser(ctx context.Context, userID, id int64) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM activities WHERE user_id=? AND id=?) OR EXISTS(SELECT 1 FROM activity_tombstones WHERE user_id=? AND activity_id=? AND deleted=1)`, userID, id, userID, id).Scan(&exists)
	return exists, err
}
