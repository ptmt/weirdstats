package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
)

type Store struct {
	db *sql.DB
}

type Activity struct {
	ID          int64
	UserID      int64
	Type        string
	Name        string
	StartTime   time.Time
	Description string
	Distance    float64
	MovingTime  int
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

type StravaToken struct {
	UserID        int64
	AccessToken   string
	RefreshToken  string
	ExpiresAt     time.Time
	UpdatedAt     time.Time
	AthleteID     int64
	AthleteName   string
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
CREATE TABLE IF NOT EXISTS activity_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	activity_id INTEGER NOT NULL,
	enqueued_at INTEGER NOT NULL,
	processed_at INTEGER
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
INSERT INTO activities (id, user_id, type, name, start_time, description, distance, moving_time, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	user_id = excluded.user_id,
	type = excluded.type,
	name = excluded.name,
	start_time = excluded.start_time,
	description = excluded.description,
	distance = excluded.distance,
	moving_time = excluded.moving_time,
	updated_at = excluded.updated_at
`, activity.ID, activity.UserID, activity.Type, activity.Name, activity.StartTime.Unix(), activity.Description, activity.Distance, activity.MovingTime, time.Now().Unix())
	} else if activity.ID != 0 {
		res, err = tx.ExecContext(ctx, `
INSERT INTO activities (id, user_id, type, name, start_time, description, distance, moving_time, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, activity.ID, activity.UserID, activity.Type, activity.Name, activity.StartTime.Unix(), activity.Description, activity.Distance, activity.MovingTime, time.Now().Unix())
	} else {
		res, err = tx.ExecContext(ctx, `
INSERT INTO activities (user_id, type, name, start_time, description, distance, moving_time, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, activity.UserID, activity.Type, activity.Name, activity.StartTime.Unix(), activity.Description, activity.Distance, activity.MovingTime, time.Now().Unix())
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
			&stopCount,
			&stopTotalSeconds,
			&trafficLightStopCount,
		); err != nil {
			return nil, err
		}
		item.StartTime = time.Unix(startTime, 0)
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
	_, err := s.db.ExecContext(ctx, `
INSERT INTO activity_stats (activity_id, stop_count, stop_total_seconds, traffic_light_stop_count, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(activity_id) DO UPDATE SET
	stop_count = excluded.stop_count,
	stop_total_seconds = excluded.stop_total_seconds,
	traffic_light_stop_count = excluded.traffic_light_stop_count,
	updated_at = excluded.updated_at
`, activityID, stats.StopCount, stats.StopTotalSeconds, stats.TrafficLightStopCount, time.Now().Unix())
	return err
}

func (s *Store) GetActivityStats(ctx context.Context, activityID int64) (stats.StopStats, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT stop_count, stop_total_seconds, traffic_light_stop_count
FROM activity_stats
WHERE activity_id = ?
`, activityID)
	var result stats.StopStats
	if err := row.Scan(&result.StopCount, &result.StopTotalSeconds, &result.TrafficLightStopCount); err != nil {
		return stats.StopStats{}, err
	}
	return result, nil
}

func (s *Store) GetActivity(ctx context.Context, activityID int64) (Activity, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, user_id, type, name, start_time, description, distance, moving_time
FROM activities
WHERE id = ?
`, activityID)
	var activity Activity
	var startTime int64
	if err := row.Scan(
		&activity.ID,
		&activity.UserID,
		&activity.Type,
		&activity.Name,
		&startTime,
		&activity.Description,
		&activity.Distance,
		&activity.MovingTime,
	); err != nil {
		return Activity{}, err
	}
	activity.StartTime = time.Unix(startTime, 0)
	return activity, nil
}
