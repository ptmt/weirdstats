package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// These migrations run after the base schema, on both existing and new databases.
func (s *Store) initSyncSchema(ctx context.Context) error {
	for _, column := range []struct{ table, name, definition string }{
		{"strava_tokens", "scopes", "TEXT NOT NULL DEFAULT ''"},
		{"strava_tokens", "connection_id", "TEXT NOT NULL DEFAULT ''"},
		{"activities", "streams_fetched", "INTEGER NOT NULL DEFAULT 0"},
		{"jobs", "user_id", "INTEGER NOT NULL DEFAULT 0"},
		{"jobs", "activity_id", "INTEGER NOT NULL DEFAULT 0"},
		{"jobs", "parent_id", "INTEGER NOT NULL DEFAULT 0"},
		{"jobs", "error_code", "TEXT NOT NULL DEFAULT ''"},
	} {
		_, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.name, column.definition))
		if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs SET user_id = COALESCE(json_extract(payload, '$.user_id'), 0),
 activity_id = COALESCE(json_extract(payload, '$.activity_id'), 0)
 WHERE user_id = 0 AND json_valid(payload);
UPDATE jobs SET user_id = COALESCE((SELECT user_id FROM activities WHERE id = jobs.activity_id), 0) WHERE user_id = 0;
CREATE INDEX IF NOT EXISTS idx_jobs_due ON jobs(status, next_run_at, id);
CREATE INDEX IF NOT EXISTS idx_jobs_user ON jobs(user_id, type, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_activity ON jobs(user_id, activity_id, type, status);
CREATE INDEX IF NOT EXISTS idx_jobs_parent ON jobs(parent_id, status);
CREATE INDEX IF NOT EXISTS idx_activities_user_time ON activities(user_id, start_time DESC, id DESC);
CREATE TABLE IF NOT EXISTS job_dispatch (user_id INTEGER PRIMARY KEY, sequence INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS processing_preferences (user_id INTEGER PRIMARY KEY, external_maps INTEGER NOT NULL DEFAULT 0, auto_publish INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS activity_route_previews (activity_id INTEGER PRIMARY KEY, points TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS api_cooldowns (provider TEXT PRIMARY KEY, until_unix INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS activity_tombstones (user_id INTEGER NOT NULL, activity_id INTEGER NOT NULL, version INTEGER NOT NULL DEFAULT 1, deleted INTEGER NOT NULL DEFAULT 1, PRIMARY KEY(user_id, activity_id));
`)
	if err != nil {
		return err
	}
	// Old workers must not recreate orphaned GPS-derived records after deletion.
	for _, table := range []string{"activity_route_previews", "activity_points", "activity_stops", "activity_stats", "activity_detected_facts", "activity_fact_metrics"} {
		_, err := s.db.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS guard_`+table+` BEFORE INSERT ON `+table+`
WHEN NOT EXISTS(SELECT 1 FROM activities WHERE id=NEW.activity_id)
BEGIN SELECT RAISE(ABORT, 'activity was removed'); END;`)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) APICooldown(ctx context.Context, provider string) (time.Time, error) {
	var until int64
	err := s.db.QueryRowContext(ctx, `SELECT until_unix FROM api_cooldowns WHERE provider = ?`, provider).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return time.Unix(until, 0), err
}

func (s *Store) SetAPICooldown(ctx context.Context, provider string, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_cooldowns(provider, until_unix) VALUES (?, ?)
ON CONFLICT(provider) DO UPDATE SET until_unix = MAX(until_unix, excluded.until_unix)`, provider, until.Unix())
	return err
}

// Waiting on a provider or authorization does not consume the failure budget.
func (s *Store) DeferJob(ctx context.Context, id int64, cursor, status, code, message string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, cursor=?, error_code=?, last_error=?, next_run_at=?, updated_at=? WHERE id=?`,
		status, cursor, code, message, next.Unix(), time.Now().Unix(), id)
	return err
}

func (s *Store) SetJobErrorCode(ctx context.Context, id int64, code string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET error_code=? WHERE id=?`, code, id)
	return err
}

func (s *Store) ListUserJobs(ctx context.Context, userID int64, activityJobs bool, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 20
	}
	op := "!="
	if activityJobs {
		op = "="
	}
	return s.listJobs(ctx, `SELECT id,type,status,payload,cursor,attempts,max_attempts,last_error,next_run_at,created_at,updated_at,user_id,activity_id,parent_id,error_code
FROM jobs WHERE user_id=? AND type `+op+` 'process_activity' ORDER BY updated_at DESC,id DESC LIMIT ?`, userID, limit)
}

func (s *Store) UserJobErrors(ctx context.Context, userID int64, limit int) ([]Job, error) {
	return s.listJobs(ctx, `SELECT id,type,status,payload,cursor,attempts,max_attempts,last_error,next_run_at,created_at,updated_at,user_id,activity_id,parent_id,error_code
FROM jobs WHERE user_id=? AND status IN ('failed','blocked','retry') ORDER BY updated_at DESC,id DESC LIMIT ?`, userID, limit)
}

type SyncStatus struct {
	Enriching   int   `json:"enriching"`
	Publishing  int   `json:"publishing"`
	Pending     int   `json:"pending"`
	Completed   int   `json:"completed"`
	Failed      int   `json:"failed"`
	Blocked     int   `json:"blocked"`
	Waiting     int   `json:"waiting"`
	Discovering int   `json:"discovering"`
	NextRunAt   int64 `json:"next_run_at,omitempty"`
}

func (s *Store) UserSyncStatus(ctx context.Context, userID int64) (SyncStatus, error) {
	var status SyncStatus
	err := s.db.QueryRowContext(ctx, `SELECT
COALESCE(SUM(type='process_activity' AND status IN ('queued','running','retry','waiting')),0),
COUNT(DISTINCT CASE WHEN type='process_activity' AND status='completed' THEN activity_id END),
COALESCE(SUM(status='failed'),0), COALESCE(SUM(status='blocked'),0), COALESCE(SUM(status='waiting'),0),
COALESCE(SUM(type IN ('sync_activities_since','sync_latest') AND status IN ('queued','running','retry','waiting')),0),
COALESCE(MIN(CASE WHEN status IN ('waiting','retry') THEN next_run_at END),0),
COALESCE(SUM(type='enrich_activity' AND status IN ('queued','running','retry')),0),
COALESCE(SUM(type='apply_activity_rules' AND status IN ('queued','running','retry','waiting')),0)
FROM jobs WHERE user_id=?`, userID).Scan(&status.Pending, &status.Completed, &status.Failed, &status.Blocked, &status.Waiting, &status.Discovering, &status.NextRunAt, &status.Enriching, &status.Publishing)
	return status, err
}

func (s *Store) RetryUserJobs(ctx context.Context, userID int64, includeBlocked bool) error {
	statuses := "'failed'"
	if includeBlocked {
		statuses += ",'blocked'"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='queued', attempts=0, next_run_at=?, updated_at=? WHERE user_id=? AND status IN (`+statuses+`)`, time.Now().Unix(), time.Now().Unix(), userID)
	return err
}

func (s *Store) ActivityStreamsFetched(ctx context.Context, id int64) (bool, error) {
	var fetched bool
	err := s.db.QueryRowContext(ctx, `SELECT streams_fetched OR EXISTS(SELECT 1 FROM activity_points WHERE activity_id=?) FROM activities WHERE id=?`, id, id).Scan(&fetched)
	return fetched, err
}

func (s *Store) MarkActivityStreamsFetched(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE activities SET streams_fetched=1 WHERE id=?`, id)
	return err
}

var ErrProcessingDisabled = errors.New("optional processing was disabled")

type automaticPublishKey struct{}

func ContextWithAutomaticPublish(ctx context.Context, automatic bool) context.Context {
	return context.WithValue(ctx, automaticPublishKey{}, automatic)
}
func IsAutomaticPublish(ctx context.Context) bool {
	value, _ := ctx.Value(automaticPublishKey{}).(bool)
	return value
}

var ErrConnectionChanged = errors.New("Strava connection changed or was removed")

type jobContextKey struct{}

type backfillContextKey struct{}

func ContextWithBackfill(ctx context.Context, backfill bool) context.Context {
	return context.WithValue(ctx, backfillContextKey{}, backfill)
}
func IsBackfill(ctx context.Context) bool {
	value, _ := ctx.Value(backfillContextKey{}).(bool)
	return value
}

func ContextWithJob(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, jobContextKey{}, id)
}

type ProcessingPreferences struct {
	ExternalMaps bool `json:"external_maps"`
	AutoPublish  bool `json:"auto_publish"`
}

func (s *Store) ProcessingPreferences(ctx context.Context, userID int64) (ProcessingPreferences, error) {
	var prefs ProcessingPreferences
	err := s.db.QueryRowContext(ctx, `SELECT external_maps,auto_publish FROM processing_preferences WHERE user_id=?`, userID).Scan(&prefs.ExternalMaps, &prefs.AutoPublish)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return prefs, err
}

func (s *Store) SaveProcessingPreferences(ctx context.Context, userID int64, prefs ProcessingPreferences) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO processing_preferences(user_id,external_maps,auto_publish) VALUES(?,?,?)
ON CONFLICT(user_id) DO UPDATE SET external_maps=excluded.external_maps,auto_publish=excluded.auto_publish`, userID, prefs.ExternalMaps, prefs.AutoPublish)
	return err
}

func (s *Store) EnqueueSyncPage(ctx context.Context, parent Job, children []Job, cursor, status string, next time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM jobs WHERE id=? AND status='running'", parent.ID).Scan(&exists); err != nil {
		return err
	}
	for _, child := range children {
		if _, err = createJob(ctx, tx, child); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET cursor=?,status=?,next_run_at=?,last_error='',error_code='',attempts=0,updated_at=? WHERE id=?`, cursor, status, next.Unix(), time.Now().Unix(), parent.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// Refresh must never recreate credentials after disconnect or replace a new grant.
func (s *Store) RefreshStravaToken(ctx context.Context, token StravaToken) error {
	res, err := s.db.ExecContext(ctx, `UPDATE strava_tokens SET access_token=?,refresh_token=?,expires_at=?,updated_at=? WHERE user_id=? AND connection_id=?`,
		token.AccessToken, token.RefreshToken, token.ExpiresAt.Unix(), time.Now().Unix(), token.UserID, token.ConnectionID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrConnectionChanged
	}
	return err
}
