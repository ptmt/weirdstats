package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

type AutomationEvent struct {
	ID           int64
	UserID       int64
	ActivityID   int64
	ActivityName string
	RuleID       int64
	Name         string
	Action       string
	Source       string
	Status       string
	Detail       string
	Triggered    bool
	CreatedAt    time.Time
}

func (s *Store) RecordAutomationEvents(ctx context.Context, userID, activityID int64, events []AutomationEvent) error {
	_, err := s.StartAutomationEvents(ctx, userID, activityID, events)
	return err
}

func (s *Store) StartAutomationEvents(ctx context.Context, userID, activityID int64, events []AutomationEvent) ([]int64, error) {
	if len(events) == 0 {
		return nil, nil
	}
	if userID <= 0 || activityID <= 0 {
		return nil, errors.New("automation owner and activity required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := validateActivityWrite(ctx, tx, userID, activityID); err != nil {
		return nil, err
	}
	var present int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM activities WHERE id=? AND user_id=?", activityID, userID).Scan(&present); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(events))
	for _, event := range events {
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now()
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO automation_events
(user_id,activity_id,rule_id,name,action,source,status,detail,triggered,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`, userID, activityID, event.RuleID, event.Name, event.Action, event.Source, event.Status, event.Detail, boolToInt(event.Triggered), event.CreatedAt.Unix())
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// Finalize existing history rows even when a normal Strava update webhook has
// invalidated the activity cache. Deleted rows are never recreated.
func (s *Store) FinishAutomationEvents(ctx context.Context, userID, activityID int64, ids []int64, events []AutomationEvent) error {
	if len(ids) != len(events) {
		return errors.New("automation event count changed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		event := events[i]
		if _, err := tx.ExecContext(ctx, `UPDATE automation_events SET status=?,detail=?,triggered=?,source=? WHERE id=? AND user_id=? AND activity_id=?`, event.Status, event.Detail, boolToInt(event.Triggered), event.Source, id, userID, activityID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListAutomationEvents(ctx context.Context, userID, activityID, beforeID int64, limit int) ([]AutomationEvent, error) {
	if limit <= 0 || limit > 101 {
		limit = 51
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.user_id,e.activity_id,a.name,e.rule_id,e.name,e.action,e.source,e.status,e.detail,e.triggered,e.created_at
FROM automation_events e JOIN activities a ON a.id=e.activity_id AND a.user_id=e.user_id
WHERE e.user_id=? AND (?=0 OR e.activity_id=?) AND (?=0 OR e.id<?)
ORDER BY e.id DESC LIMIT ?`, userID, activityID, activityID, beforeID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AutomationEvent
	for rows.Next() {
		var event AutomationEvent
		var timestamp int64
		if err := rows.Scan(&event.ID, &event.UserID, &event.ActivityID, &event.ActivityName, &event.RuleID, &event.Name, &event.Action, &event.Source, &event.Status, &event.Detail, &event.Triggered, &timestamp); err != nil {
			return nil, err
		}
		event.CreatedAt = time.Unix(timestamp, 0)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) CountTriggeredAutomations(ctx context.Context, userID int64, activityIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int)
	if len(activityIDs) == 0 {
		return counts, nil
	}
	args := []any{userID}
	for _, id := range activityIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT activity_id,COUNT(*) FROM automation_events WHERE user_id=? AND triggered=1 AND activity_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(activityIDs)), ",")+`) GROUP BY activity_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}
