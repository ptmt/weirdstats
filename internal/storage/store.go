package storage

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
)

type Store struct {
	db *sql.DB
}

type Activity struct {
	ID           int64
	UserID       int64
	Type         string
	Name         string
	StartTime    time.Time
	Description  string
	Distance     float64
	MovingTime   int
	AveragePower float64
	Visibility   string
	IsPrivate    bool
	HideFromHome bool
	HiddenByRule bool
	UpdatedAt    time.Time
}

type WebhookEvent struct {
	ID         int64
	ObjectID   int64
	ObjectType string
	AspectType string
	OwnerID    int64
	RawPayload string
	ReceivedAt time.Time
}

type Job struct {
	ID          int64
	Type        string
	Status      string
	Payload     string
	Cursor      string
	Attempts    int
	MaxAttempts int
	LastError   string
	NextRunAt   time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type StravaToken struct {
	UserID       int64
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	UpdatedAt    time.Time
	AthleteID    int64
	AthleteName  string
}

type HideRule struct {
	ID        int64
	UserID    int64
	Name      string
	Condition string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ActivityWithStats struct {
	Activity
	StopCount             int
	StopTotalSeconds      int
	TrafficLightStopCount int
	HasStats              bool
}

type ActivityStop struct {
	Seq             int
	Lat             float64
	Lon             float64
	StartSeconds    float64
	DurationSeconds int
	HasTrafficLight bool
	HasRoadCrossing bool
	CrossingRoad    string
}

type ActivityTime struct {
	StartTime  time.Time
	MovingTime int
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) InitSchema(ctx context.Context) error {
	// Run migrations for existing databases
	migrations := []string{
		`ALTER TABLE strava_tokens ADD COLUMN athlete_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE strava_tokens ADD COLUMN athlete_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE activities ADD COLUMN distance REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE activities ADD COLUMN moving_time INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE activities ADD COLUMN average_power REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE activities ADD COLUMN visibility TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE activities ADD COLUMN is_private INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE activities ADD COLUMN hide_from_home INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE activities ADD COLUMN hidden_by_rule INTEGER NOT NULL DEFAULT 0`,
	}
	for _, m := range migrations {
		_, _ = s.db.ExecContext(ctx, m) // ignore errors (column already exists)
	}

	schema := `
CREATE TABLE IF NOT EXISTS activities (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL,
	type TEXT NOT NULL,
	name TEXT NOT NULL,
	start_time INTEGER NOT NULL,
	description TEXT NOT NULL,
	distance REAL NOT NULL DEFAULT 0,
	moving_time INTEGER NOT NULL DEFAULT 0,
	average_power REAL NOT NULL DEFAULT 0,
	visibility TEXT NOT NULL DEFAULT '',
	is_private INTEGER NOT NULL DEFAULT 0,
	hide_from_home INTEGER NOT NULL DEFAULT 0,
	hidden_by_rule INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS activity_points (
	activity_id INTEGER NOT NULL,
	seq INTEGER NOT NULL,
	lat REAL NOT NULL,
	lon REAL NOT NULL,
	ts INTEGER NOT NULL,
	speed REAL NOT NULL,
	PRIMARY KEY (activity_id, seq)
);
CREATE TABLE IF NOT EXISTS activity_stats (
	activity_id INTEGER PRIMARY KEY,
	stop_count INTEGER NOT NULL,
	stop_total_seconds INTEGER NOT NULL,
	traffic_light_stop_count INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS activity_stops (
	activity_id INTEGER NOT NULL,
	seq INTEGER NOT NULL,
	lat REAL NOT NULL,
	lon REAL NOT NULL,
	start_seconds REAL NOT NULL,
	duration_seconds INTEGER NOT NULL,
	has_traffic_light INTEGER NOT NULL,
	has_road_crossing INTEGER NOT NULL,
	crossing_road TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (activity_id, seq)
);
CREATE TABLE IF NOT EXISTS activity_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	activity_id INTEGER NOT NULL,
	enqueued_at INTEGER NOT NULL,
	processed_at INTEGER
);
CREATE TABLE IF NOT EXISTS jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	status TEXT NOT NULL,
	payload TEXT NOT NULL,
	cursor TEXT NOT NULL,
	attempts INTEGER NOT NULL,
	max_attempts INTEGER NOT NULL,
	last_error TEXT NOT NULL,
	next_run_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS webhook_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	object_id INTEGER NOT NULL,
	object_type TEXT NOT NULL,
	aspect_type TEXT NOT NULL,
	owner_id INTEGER NOT NULL,
	raw_payload TEXT NOT NULL,
	received_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS strava_tokens (
	user_id INTEGER PRIMARY KEY,
	access_token TEXT NOT NULL,
	refresh_token TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	athlete_id INTEGER NOT NULL DEFAULT 0,
	athlete_name TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS hide_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	condition TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *Store) InsertActivity(ctx context.Context, activity Activity, points []gps.Point) (int64, error) {
	if activity.StartTime.IsZero() {
		return 0, errors.New("activity start time required")
	}
	if activity.Type == "" {
		return 0, errors.New("activity type required")
	}
	if activity.Name == "" {
		return 0, errors.New("activity name required")
	}

	return s.upsertActivityWithPoints(ctx, activity, points, false)
}

func (s *Store) UpsertActivity(ctx context.Context, activity Activity, points []gps.Point) (int64, error) {
	if activity.StartTime.IsZero() {
		return 0, errors.New("activity start time required")
	}
	if activity.Type == "" {
		return 0, errors.New("activity type required")
	}
	if activity.Name == "" {
		return 0, errors.New("activity name required")
	}

	return s.upsertActivityWithPoints(ctx, activity, points, true)
}

func (s *Store) upsertActivityWithPoints(ctx context.Context, activity Activity, points []gps.Point, allowUpsert bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var res sql.Result
	if allowUpsert && activity.ID != 0 {
		res, err = tx.ExecContext(ctx, `
INSERT INTO activities (id, user_id, type, name, start_time, description, distance, moving_time, average_power, visibility, is_private, hide_from_home, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	user_id = excluded.user_id,
	type = excluded.type,
	name = excluded.name,
	start_time = excluded.start_time,
	description = excluded.description,
	distance = excluded.distance,
	moving_time = excluded.moving_time,
	average_power = excluded.average_power,
	visibility = excluded.visibility,
	is_private = excluded.is_private,
	hide_from_home = excluded.hide_from_home,
	updated_at = excluded.updated_at
`, activity.ID, activity.UserID, activity.Type, activity.Name, activity.StartTime.Unix(), activity.Description, activity.Distance, activity.MovingTime, activity.AveragePower, activity.Visibility, boolToInt(activity.IsPrivate), boolToInt(activity.HideFromHome), time.Now().Unix())
	} else if activity.ID != 0 {
		res, err = tx.ExecContext(ctx, `
INSERT INTO activities (id, user_id, type, name, start_time, description, distance, moving_time, average_power, visibility, is_private, hide_from_home, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, activity.ID, activity.UserID, activity.Type, activity.Name, activity.StartTime.Unix(), activity.Description, activity.Distance, activity.MovingTime, activity.AveragePower, activity.Visibility, boolToInt(activity.IsPrivate), boolToInt(activity.HideFromHome), time.Now().Unix())
	} else {
		res, err = tx.ExecContext(ctx, `
INSERT INTO activities (user_id, type, name, start_time, description, distance, moving_time, average_power, visibility, is_private, hide_from_home, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, activity.UserID, activity.Type, activity.Name, activity.StartTime.Unix(), activity.Description, activity.Distance, activity.MovingTime, activity.AveragePower, activity.Visibility, boolToInt(activity.IsPrivate), boolToInt(activity.HideFromHome), time.Now().Unix())
	}
	if err != nil {
		return 0, err
	}

	activityID := activity.ID
	if activityID == 0 {
		activityID, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	}
	if allowUpsert {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM activity_points
WHERE activity_id = ?
`, activityID); err != nil {
			return 0, err
		}
	}

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO activity_points (activity_id, seq, lat, lon, ts, speed)
VALUES (?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for i, p := range points {
		_, err = stmt.ExecContext(ctx, activityID, i, p.Lat, p.Lon, p.Time.Unix(), p.Speed)
		if err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return activityID, nil
}

func (s *Store) EnqueueActivity(ctx context.Context, activityID int64) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO activity_queue (activity_id, enqueued_at)
VALUES (?, ?)
`, activityID, time.Now().Unix())
	return err
}

func (s *Store) HasActivity(ctx context.Context, activityID int64) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT 1
FROM activities
WHERE id = ?
`, activityID)
	var marker int
	if err := row.Scan(&marker); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) CountActivityPoints(ctx context.Context, activityID int64) (int, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM activity_points
WHERE activity_id = ?
`, activityID)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountQueue(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM activity_queue
WHERE processed_at IS NULL
`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM strava_tokens
`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListActivityTimes(ctx context.Context, userID int64, start, end time.Time) ([]ActivityTime, error) {
	if userID == 0 {
		userID = 1
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT start_time, moving_time
FROM activities
WHERE user_id = ?
	AND start_time >= ?
	AND start_time < ?
ORDER BY start_time
`, userID, start.Unix(), end.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []ActivityTime
	for rows.Next() {
		var item ActivityTime
		var startTime int64
		if err := rows.Scan(&startTime, &item.MovingTime); err != nil {
			return nil, err
		}
		item.StartTime = time.Unix(startTime, 0)
		activities = append(activities, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return activities, nil
}

func (s *Store) ListActivityYears(ctx context.Context, userID int64) ([]int, error) {
	if userID == 0 {
		userID = 1
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT strftime('%Y', start_time, 'unixepoch') AS year
FROM activities
WHERE user_id = ?
ORDER BY year DESC
`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var years []int
	for rows.Next() {
		var yearStr string
		if err := rows.Scan(&yearStr); err != nil {
			return nil, err
		}
		year, err := strconv.Atoi(yearStr)
		if err != nil {
			return nil, err
		}
		years = append(years, year)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return years, nil
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, type, status, payload, cursor, attempts, max_attempts, last_error, next_run_at, created_at, updated_at
FROM jobs
ORDER BY updated_at DESC, id DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		var nextRunAt int64
		var createdAt int64
		var updatedAt int64
		if err := rows.Scan(&job.ID, &job.Type, &job.Status, &job.Payload, &job.Cursor, &job.Attempts, &job.MaxAttempts, &job.LastError,
			&nextRunAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		job.NextRunAt = time.Unix(nextRunAt, 0)
		job.CreatedAt = time.Unix(createdAt, 0)
		job.UpdatedAt = time.Unix(updatedAt, 0)
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) CreateJob(ctx context.Context, job Job) (int64, error) {
	if job.Type == "" {
		return 0, errors.New("job type required")
	}
	if job.Status == "" {
		job.Status = "queued"
	}
	if job.Payload == "" {
		job.Payload = "{}"
	}
	if job.Cursor == "" {
		job.Cursor = "{}"
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 10
	}
	if job.NextRunAt.IsZero() {
		job.NextRunAt = time.Now()
	}
	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO jobs (type, status, payload, cursor, attempts, max_attempts, last_error, next_run_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, job.Type, job.Status, job.Payload, job.Cursor, job.Attempts, job.MaxAttempts, job.LastError,
		job.NextRunAt.Unix(), job.CreatedAt.Unix(), job.UpdatedAt.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ClaimJob(ctx context.Context, now time.Time, staleAfter time.Duration) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	staleCutoff := now.Add(-staleAfter).Unix()
	row := tx.QueryRowContext(ctx, `
SELECT id, type, status, payload, cursor, attempts, max_attempts, last_error, next_run_at, created_at, updated_at
FROM jobs
WHERE (
	(status IN ('queued', 'retry') AND next_run_at <= ?)
	OR (status = 'running' AND updated_at <= ?)
)
ORDER BY next_run_at, id
LIMIT 1
`, now.Unix(), staleCutoff)

	var job Job
	var nextRunAt int64
	var createdAt int64
	var updatedAt int64
	if err = row.Scan(&job.ID, &job.Type, &job.Status, &job.Payload, &job.Cursor, &job.Attempts, &job.MaxAttempts, &job.LastError,
		&nextRunAt, &createdAt, &updatedAt); err != nil {
		_ = tx.Rollback()
		return Job{}, err
	}
	job.NextRunAt = time.Unix(nextRunAt, 0)
	job.CreatedAt = time.Unix(createdAt, 0)
	job.UpdatedAt = time.Unix(updatedAt, 0)

	job.Attempts++
	job.Status = "running"
	job.UpdatedAt = now
	if _, err = tx.ExecContext(ctx, `
UPDATE jobs
SET status = ?, attempts = ?, updated_at = ?
WHERE id = ?
`, job.Status, job.Attempts, job.UpdatedAt.Unix(), job.ID); err != nil {
		_ = tx.Rollback()
		return Job{}, err
	}

	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Store) MarkJobQueued(ctx context.Context, jobID int64, cursor string, nextRunAt time.Time) error {
	if nextRunAt.IsZero() {
		nextRunAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs
SET status = 'queued',
	cursor = ?,
	last_error = '',
	next_run_at = ?,
	updated_at = ?
WHERE id = ?
`, cursor, nextRunAt.Unix(), time.Now().Unix(), jobID)
	return err
}

func (s *Store) MarkJobRetry(ctx context.Context, jobID int64, cursor string, lastError string, nextRunAt time.Time) error {
	if nextRunAt.IsZero() {
		nextRunAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs
SET status = 'retry',
	cursor = ?,
	last_error = ?,
	next_run_at = ?,
	updated_at = ?
WHERE id = ?
`, cursor, lastError, nextRunAt.Unix(), time.Now().Unix(), jobID)
	return err
}

func (s *Store) MarkJobFailed(ctx context.Context, jobID int64, cursor string, lastError string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs
SET status = 'failed',
	cursor = ?,
	last_error = ?,
	updated_at = ?
WHERE id = ?
`, cursor, lastError, time.Now().Unix(), jobID)
	return err
}

func (s *Store) MarkJobCompleted(ctx context.Context, jobID int64, cursor string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs
SET status = 'completed',
	cursor = ?,
	last_error = '',
	updated_at = ?
WHERE id = ?
`, cursor, time.Now().Unix(), jobID)
	return err
}

func (s *Store) InsertWebhookEvent(ctx context.Context, event WebhookEvent) (int64, error) {
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO webhook_events (object_id, object_type, aspect_type, owner_id, raw_payload, received_at)
VALUES (?, ?, ?, ?, ?, ?)
`, event.ObjectID, event.ObjectType, event.AspectType, event.OwnerID, event.RawPayload, event.ReceivedAt.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) CountWebhookEvents(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM webhook_events
`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) UpsertStravaToken(ctx context.Context, token StravaToken) error {
	if token.UserID == 0 {
		token.UserID = 1
	}
	if token.UpdatedAt.IsZero() {
		token.UpdatedAt = time.Now()
	}
	if token.ExpiresAt.IsZero() {
		token.ExpiresAt = time.Now().Add(-time.Minute)
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO strava_tokens (user_id, access_token, refresh_token, expires_at, updated_at, athlete_id, athlete_name)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET
	access_token = excluded.access_token,
	refresh_token = excluded.refresh_token,
	expires_at = excluded.expires_at,
	updated_at = excluded.updated_at,
	athlete_id = CASE WHEN excluded.athlete_id != 0 THEN excluded.athlete_id ELSE strava_tokens.athlete_id END,
	athlete_name = CASE WHEN excluded.athlete_name != '' THEN excluded.athlete_name ELSE strava_tokens.athlete_name END
`, token.UserID, token.AccessToken, token.RefreshToken, token.ExpiresAt.Unix(), token.UpdatedAt.Unix(), token.AthleteID, token.AthleteName)
	return err
}

func (s *Store) GetStravaToken(ctx context.Context, userID int64) (StravaToken, error) {
	if userID == 0 {
		userID = 1
	}
	row := s.db.QueryRowContext(ctx, `
SELECT access_token, refresh_token, expires_at, updated_at, athlete_id, athlete_name
FROM strava_tokens
WHERE user_id = ?
`, userID)
	var token StravaToken
	token.UserID = userID
	var expiresAt int64
	var updatedAt int64
	if err := row.Scan(&token.AccessToken, &token.RefreshToken, &expiresAt, &updatedAt, &token.AthleteID, &token.AthleteName); err != nil {
		return StravaToken{}, err
	}
	token.ExpiresAt = time.Unix(expiresAt, 0)
	token.UpdatedAt = time.Unix(updatedAt, 0)
	return token, nil
}

func (s *Store) DeleteStravaToken(ctx context.Context, userID int64) error {
	if userID == 0 {
		userID = 1
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM strava_tokens WHERE user_id = ?`, userID)
	return err
}

func (s *Store) ListHideRules(ctx context.Context, userID int64) ([]HideRule, error) {
	if userID == 0 {
		userID = 1
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, user_id, name, condition, enabled, created_at, updated_at
FROM hide_rules
WHERE user_id = ?
ORDER BY created_at DESC
`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []HideRule
	for rows.Next() {
		var rule HideRule
		var enabled int
		var createdAt int64
		var updatedAt int64
		if err := rows.Scan(
			&rule.ID,
			&rule.UserID,
			&rule.Name,
			&rule.Condition,
			&enabled,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		rule.Enabled = enabled != 0
		rule.CreatedAt = time.Unix(createdAt, 0)
		rule.UpdatedAt = time.Unix(updatedAt, 0)
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func (s *Store) CreateHideRule(ctx context.Context, rule HideRule) (int64, error) {
	if rule.UserID == 0 {
		rule.UserID = 1
	}
	now := time.Now()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = now
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO hide_rules (user_id, name, condition, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
`, rule.UserID, rule.Name, rule.Condition, boolToInt(rule.Enabled), rule.CreatedAt.Unix(), rule.UpdatedAt.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateHideRuleEnabled(ctx context.Context, ruleID int64, enabled bool) error {
	if ruleID == 0 {
		return errors.New("rule id required")
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE hide_rules
SET enabled = ?, updated_at = ?
WHERE id = ?
`, boolToInt(enabled), time.Now().Unix(), ruleID)
	return err
}

func (s *Store) DeleteHideRule(ctx context.Context, ruleID int64) error {
	if ruleID == 0 {
		return errors.New("rule id required")
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM hide_rules
WHERE id = ?
`, ruleID)
	return err
}

func (s *Store) DeleteUserData(ctx context.Context, userID int64) error {
	if userID == 0 {
		userID = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
DELETE FROM activity_points
WHERE activity_id IN (SELECT id FROM activities WHERE user_id = ?)
`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM activity_stats
WHERE activity_id IN (SELECT id FROM activities WHERE user_id = ?)
`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM activity_queue
WHERE activity_id IN (SELECT id FROM activities WHERE user_id = ?)
`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM activities
WHERE user_id = ?
`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM webhook_events
WHERE owner_id = ?
`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM strava_tokens
WHERE user_id = ?
`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM hide_rules
WHERE user_id = ?
`, userID); err != nil {
		return err
	}

	return tx.Commit()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) DequeueActivity(ctx context.Context) (queueID int64, activityID int64, err error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, activity_id
FROM activity_queue
WHERE processed_at IS NULL
ORDER BY id
LIMIT 1
`)
	if err := row.Scan(&queueID, &activityID); err != nil {
		return 0, 0, err
	}
	return queueID, activityID, nil
}

func (s *Store) MarkProcessed(ctx context.Context, queueID int64) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE activity_queue
SET processed_at = ?
WHERE id = ?
`, time.Now().Unix(), queueID)
	return err
}

func (s *Store) LoadActivityPoints(ctx context.Context, activityID int64) ([]gps.Point, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT lat, lon, ts, speed
FROM activity_points
WHERE activity_id = ?
ORDER BY seq
`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []gps.Point
	for rows.Next() {
		var p gps.Point
		var ts int64
		if err := rows.Scan(&p.Lat, &p.Lon, &ts, &p.Speed); err != nil {
			return nil, err
		}
		p.Time = time.Unix(ts, 0)
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func (s *Store) LoadActivityStops(ctx context.Context, activityID int64) ([]ActivityStop, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, lat, lon, start_seconds, duration_seconds, has_traffic_light, has_road_crossing, crossing_road
FROM activity_stops
WHERE activity_id = ?
ORDER BY seq
`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stops []ActivityStop
	for rows.Next() {
		var stop ActivityStop
		var hasLight int
		var hasCrossing int
		if err := rows.Scan(
			&stop.Seq,
			&stop.Lat,
			&stop.Lon,
			&stop.StartSeconds,
			&stop.DurationSeconds,
			&hasLight,
			&hasCrossing,
			&stop.CrossingRoad,
		); err != nil {
			return nil, err
		}
		stop.HasTrafficLight = hasLight != 0
		stop.HasRoadCrossing = hasCrossing != 0
		stops = append(stops, stop)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return stops, nil
}

func (s *Store) ReplaceActivityStops(ctx context.Context, activityID int64, stops []ActivityStop, updatedAt time.Time) error {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
DELETE FROM activity_stops
WHERE activity_id = ?
`, activityID); err != nil {
		return err
	}

	if len(stops) == 0 {
		return tx.Commit()
	}

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO activity_stops (
	activity_id, seq, lat, lon, start_seconds, duration_seconds,
	has_traffic_light, has_road_crossing, crossing_road, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, stop := range stops {
		hasLight := 0
		if stop.HasTrafficLight {
			hasLight = 1
		}
		hasCrossing := 0
		if stop.HasRoadCrossing {
			hasCrossing = 1
		}
		if _, err := stmt.ExecContext(
			ctx,
			activityID,
			stop.Seq,
			stop.Lat,
			stop.Lon,
			stop.StartSeconds,
			stop.DurationSeconds,
			hasLight,
			hasCrossing,
			stop.CrossingRoad,
			updatedAt.Unix(),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) ListActivitiesWithStats(ctx context.Context, userID int64, limit int) ([]ActivityWithStats, error) {
	if userID == 0 {
		userID = 1
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id,
	a.user_id,
	a.type,
	a.name,
	a.start_time,
	a.description,
	a.distance,
	a.moving_time,
	a.average_power,
	a.visibility,
	a.is_private,
	a.hide_from_home,
	a.hidden_by_rule,
	s.stop_count,
	s.stop_total_seconds,
	s.traffic_light_stop_count
FROM activities a
LEFT JOIN activity_stats s ON s.activity_id = a.id
WHERE a.user_id = ?
ORDER BY a.start_time DESC
LIMIT ?
`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []ActivityWithStats
	for rows.Next() {
		var item ActivityWithStats
		var startTime int64
		var isPrivate int
		var hideFromHome int
		var hiddenByRule int
		var stopCount sql.NullInt64
		var stopTotalSeconds sql.NullInt64
		var trafficLightStopCount sql.NullInt64
		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.Type,
			&item.Name,
			&startTime,
			&item.Description,
			&item.Distance,
			&item.MovingTime,
			&item.AveragePower,
			&item.Visibility,
			&isPrivate,
			&hideFromHome,
			&hiddenByRule,
			&stopCount,
			&stopTotalSeconds,
			&trafficLightStopCount,
		); err != nil {
			return nil, err
		}
		item.StartTime = time.Unix(startTime, 0)
		item.IsPrivate = isPrivate != 0
		item.HideFromHome = hideFromHome != 0
		item.HiddenByRule = hiddenByRule != 0
		if stopCount.Valid {
			item.HasStats = true
			item.StopCount = int(stopCount.Int64)
			item.StopTotalSeconds = int(stopTotalSeconds.Int64)
			item.TrafficLightStopCount = int(trafficLightStopCount.Int64)
		}
		activities = append(activities, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return activities, nil
}

func (s *Store) UpsertActivityStats(ctx context.Context, activityID int64, stats stats.StopStats) error {
	updatedAt := time.Now()
	if !stats.UpdatedAt.IsZero() {
		updatedAt = stats.UpdatedAt
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO activity_stats (activity_id, stop_count, stop_total_seconds, traffic_light_stop_count, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(activity_id) DO UPDATE SET
	stop_count = excluded.stop_count,
	stop_total_seconds = excluded.stop_total_seconds,
	traffic_light_stop_count = excluded.traffic_light_stop_count,
	updated_at = excluded.updated_at
`, activityID, stats.StopCount, stats.StopTotalSeconds, stats.TrafficLightStopCount, updatedAt.Unix())
	return err
}

func (s *Store) GetActivityStats(ctx context.Context, activityID int64) (stats.StopStats, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT stop_count, stop_total_seconds, traffic_light_stop_count, updated_at
FROM activity_stats
WHERE activity_id = ?
`, activityID)
	var result stats.StopStats
	var updatedAt int64
	if err := row.Scan(&result.StopCount, &result.StopTotalSeconds, &result.TrafficLightStopCount, &updatedAt); err != nil {
		return stats.StopStats{}, err
	}
	result.UpdatedAt = time.Unix(updatedAt, 0)
	return result, nil
}

func (s *Store) GetActivity(ctx context.Context, activityID int64) (Activity, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, user_id, type, name, start_time, description, distance, moving_time, average_power, visibility, is_private, hide_from_home, hidden_by_rule, updated_at
FROM activities
WHERE id = ?
`, activityID)
	var activity Activity
	var startTime int64
	var isPrivate int
	var hideFromHome int
	var hiddenByRule int
	var updatedAt int64
	if err := row.Scan(
		&activity.ID,
		&activity.UserID,
		&activity.Type,
		&activity.Name,
		&startTime,
		&activity.Description,
		&activity.Distance,
		&activity.MovingTime,
		&activity.AveragePower,
		&activity.Visibility,
		&isPrivate,
		&hideFromHome,
		&hiddenByRule,
		&updatedAt,
	); err != nil {
		return Activity{}, err
	}
	activity.StartTime = time.Unix(startTime, 0)
	activity.IsPrivate = isPrivate != 0
	activity.HideFromHome = hideFromHome != 0
	activity.HiddenByRule = hiddenByRule != 0
	activity.UpdatedAt = time.Unix(updatedAt, 0)
	return activity, nil
}

func (s *Store) UpdateActivityHiddenByRule(ctx context.Context, activityID int64, hidden bool) error {
	if activityID == 0 {
		return errors.New("activity id required")
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE activities
SET hidden_by_rule = ?
WHERE id = ?
`, boolToInt(hidden), activityID)
	return err
}
