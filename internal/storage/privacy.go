package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type connectionKey struct{}
type connectionGuard struct {
	UserID                      int64
	ConnectionID                string
	ActivityID, ActivityVersion int64
}

func (s *Store) ActivityFetchContext(ctx context.Context, userID, activityID int64, connectionID string) (context.Context, error) {
	var version int64
	var deleted bool
	err := s.db.QueryRowContext(ctx, `SELECT version,deleted FROM activity_tombstones WHERE user_id=? AND activity_id=?`, userID, activityID).Scan(&version, &deleted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ctx, err
	}
	if deleted {
		return ctx, ErrConnectionChanged
	}
	return context.WithValue(ctx, connectionKey{}, connectionGuard{userID, connectionID, activityID, version}), nil
}

func validateActivityWrite(ctx context.Context, tx jobDB, userID, activityID int64) error {
	if claimID, ok := ctx.Value(jobContextKey{}).(int64); ok {
		var present int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM jobs WHERE id=?", claimID).Scan(&present); err != nil {
			return ErrConnectionChanged
		}
	}
	guard, guarded := ctx.Value(connectionKey{}).(connectionGuard)
	if guarded {
		var connectionID string
		if err := tx.QueryRowContext(ctx, `SELECT connection_id FROM strava_tokens WHERE user_id=?`, userID).Scan(&connectionID); err != nil {
			return ErrConnectionChanged
		}
		if userID != guard.UserID || activityID != guard.ActivityID || connectionID != guard.ConnectionID {
			return ErrConnectionChanged
		}
		var version int64
		var deleted bool
		err := tx.QueryRowContext(ctx, `SELECT version,deleted FROM activity_tombstones WHERE user_id=? AND activity_id=?`, userID, activityID).Scan(&version, &deleted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if deleted || version != guard.ActivityVersion {
			return ErrConnectionChanged
		}
	}
	var owner int64
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM activities WHERE id=?`, activityID).Scan(&owner)
	if err == nil && owner != userID {
		return fmt.Errorf("activity belongs to another user")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func HasJobContext(ctx context.Context) bool { _, ok := ctx.Value(jobContextKey{}).(int64); return ok }

func (s *Store) GuardActivityContext(ctx context.Context, id int64) (context.Context, error) {
	if guard, ok := ctx.Value(connectionKey{}).(connectionGuard); ok {
		if guard.ActivityID != id {
			return ctx, ErrConnectionChanged
		}
		return ctx, s.CheckActivityContext(ctx)
	}
	a, err := s.GetActivity(ctx, id)
	if err != nil {
		return ctx, err
	}
	token, err := s.GetStravaToken(ctx, a.UserID)
	if err != nil {
		return ctx, ErrConnectionChanged
	}
	return s.ActivityFetchContext(ctx, a.UserID, id, token.ConnectionID)
}

func validateDerivedWrite(ctx context.Context, db jobDB, id int64) error {
	if guard, ok := ctx.Value(connectionKey{}).(connectionGuard); ok {
		return validateActivityWrite(ctx, db, guard.UserID, id)
	}
	return nil
}

func (s *Store) CheckActivityContext(ctx context.Context) error {
	guard, ok := ctx.Value(connectionKey{}).(connectionGuard)
	if !ok {
		return nil
	}
	return validateActivityWrite(ctx, s.db, guard.UserID, guard.ActivityID)
}

// Invalidate cached data before accepting fresh work for a webhook. Its version
// also fences out a fetch that started before the privacy change or deletion.
func (s *Store) InvalidateActivity(ctx context.Context, userID, activityID int64, deleted bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = invalidateActivity(ctx, tx, userID, activityID, deleted); err != nil {
		return err
	}
	return tx.Commit()
}
func invalidateActivity(ctx context.Context, tx *sql.Tx, userID, activityID int64, deleted bool) error {
	var err error

	if _, err = tx.ExecContext(ctx, `INSERT INTO activity_tombstones(user_id,activity_id,version,deleted) VALUES(?,?,1,?)
ON CONFLICT(user_id,activity_id) DO UPDATE SET version=version+1,deleted=excluded.deleted`, userID, activityID, deleted); err != nil {
		return err
	}
	for _, table := range []string{"activity_route_previews", "activity_points", "activity_stops", "activity_stats", "activity_detected_facts", "activity_fact_metrics", "activity_queue"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE activity_id=? AND EXISTS(SELECT 1 FROM activities WHERE id=? AND user_id=?)`, activityID, activityID, userID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM webhook_events WHERE owner_id=? AND object_type='activity' AND object_id=?`, userID, activityID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM jobs WHERE user_id=? AND activity_id=?`, userID, activityID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM activities WHERE id=? AND user_id=?`, activityID, userID); err != nil {
		return err
	}
	return nil
}

// Retain preferences and the current OAuth grant while removing imported data.
func (s *Store) PurgeImportedData(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = purgeImportedData(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}
func purgeImportedData(ctx context.Context, tx *sql.Tx, userID int64) error {
	var err error
	for _, table := range []string{"activity_route_previews", "activity_points", "activity_stops", "activity_stats", "activity_detected_facts", "activity_fact_metrics", "activity_queue"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE activity_id IN(SELECT id FROM activities WHERE user_id=?)`, userID); err != nil {
			return err
		}
	}
	for _, table := range []string{"activities", "jobs"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id=?`, userID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM webhook_events WHERE owner_id=?`, userID); err != nil {
		return err
	}
	return nil
}

// Store the grant, remove data from broader permissions, and start recovery as
// one operation. A reconnect cannot leave a half-finished privacy downgrade.
func (s *Store) CompleteStravaConnection(ctx context.Context, token StravaToken, purge bool, initial *Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = upsertStravaToken(ctx, tx, token); err != nil {
		return err
	}
	if purge {
		if err = purgeImportedData(ctx, tx, token.UserID); err != nil {
			return err
		}
	}
	if initial != nil {
		if _, err = createJob(ctx, tx, *initial); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) HasImportedActivities(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM activities WHERE user_id=?)", userID).Scan(&exists)
	return exists, err
}
func (s *Store) updateActivityGuarded(ctx context.Context, id int64, query string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = validateDerivedWrite(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}
