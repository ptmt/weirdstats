package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ApplyStravaWebhook serializes permission checks, invalidation and enqueueing
// with disconnect/account deletion. A delayed event cannot recreate removed work.
func (s *Store) ApplyStravaWebhook(ctx context.Context, event WebhookEvent, deauthorized bool, job *Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var connected int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM strava_tokens WHERE user_id=?", event.OwnerID).Scan(&connected); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if deauthorized {
		if err = deleteUserData(ctx, tx, event.OwnerID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if event.ObjectType == "activity" && (event.AspectType == "delete" || event.AspectType == "create" || event.AspectType == "update") {
		if err = invalidateActivity(ctx, tx, event.OwnerID, event.ObjectID, event.AspectType == "delete"); err != nil {
			return err
		}
		if event.AspectType == "delete" {
			return tx.Commit()
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO webhook_events(object_id,object_type,aspect_type,owner_id,raw_payload,received_at) VALUES(?,?,?,?,?,?)`, event.ObjectID, event.ObjectType, event.AspectType, event.OwnerID, event.RawPayload, time.Now().Unix()); err != nil {
		return err
	}
	if job != nil {
		if _, err = createJob(ctx, tx, *job); err != nil {
			return err
		}
	}
	return tx.Commit()
}
