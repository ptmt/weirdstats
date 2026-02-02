package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"weirdstats/internal/ingest"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

const (
	JobTypeSyncActivitiesSince = "sync_activities_since"
	JobTypeSyncLatest          = "sync_latest"
)

type SyncSincePayload struct {
	UserID    int64 `json:"user_id"`
	AfterUnix int64 `json:"after_unix"`
	PerPage   int   `json:"per_page"`
}

type SyncSinceCursor struct {
	Page       int   `json:"page"`
	Enqueued   int   `json:"enqueued"`
	BeforeUnix int64 `json:"before_unix"`
}

type SyncLatestPayload struct {
	UserID int64 `json:"user_id"`
}

type SyncLatestCursor struct {
	Enqueued int `json:"enqueued"`
}

type Runner struct {
	Store        *storage.Store
	Ingestor     *ingest.Ingestor
	PollInterval time.Duration
	StaleAfter   time.Duration
}

func (r *Runner) ProcessNext(ctx context.Context) (bool, error) {
	if r.Store == nil {
		return false, fmt.Errorf("job store not configured")
	}
	job, err := r.Store.ClaimJob(ctx, time.Now(), r.staleAfter())
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	if job.MaxAttempts > 0 && job.Attempts > job.MaxAttempts {
		if err := r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "max attempts exceeded"); err != nil {
			return true, err
		}
		return true, nil
	}

	switch job.Type {
	case JobTypeSyncActivitiesSince:
		if err := r.handleSyncSince(ctx, job); err != nil {
			return true, err
		}
	case JobTypeSyncLatest:
		if err := r.handleSyncLatest(ctx, job); err != nil {
			return true, err
		}
	default:
		if err := r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "unknown job type"); err != nil {
			return true, err
		}
	}

	return true, nil
}

func (r *Runner) handleSyncSince(ctx context.Context, job storage.Job) error {
	payload, err := parseSyncSincePayload(job.Payload)
	if err != nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, fmt.Sprintf("invalid payload: %v", err))
	}
	cursor, err := parseSyncSinceCursor(job.Cursor)
	if err != nil {
		log.Printf("job %d: invalid cursor, resetting: %v", job.ID, err)
		cursor = SyncSinceCursor{Page: 1}
	}

	if cursor.Page <= 0 {
		cursor.Page = 1
	}
	perPage := payload.PerPage
	if perPage <= 0 {
		perPage = 100
	}
	if cursor.BeforeUnix <= 0 {
		cursor.BeforeUnix = time.Now().Unix()
	}

	if r.Ingestor == nil || r.Ingestor.Strava == nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "strava client not configured")
	}

	after := time.Unix(payload.AfterUnix, 0)
	before := time.Unix(cursor.BeforeUnix, 0)
	activities, err := r.Ingestor.Strava.ListActivities(ctx, after, before, cursor.Page, perPage)
	if err != nil {
		return r.markJobRetry(ctx, job, cursor, err)
	}

	if len(activities) == 0 {
		cursorJSON, _ := json.Marshal(cursor)
		return r.Store.MarkJobCompleted(ctx, job.ID, string(cursorJSON))
	}

	oldestStart := activities[0].StartDate
	for _, activity := range activities {
		if err := r.Store.EnqueueActivity(ctx, activity.ID); err != nil {
			return r.markJobRetry(ctx, job, cursor, err)
		}
		cursor.Enqueued++
		if activity.StartDate.Before(oldestStart) {
			oldestStart = activity.StartDate
		}
	}

	oldestUnix := oldestStart.Unix()
	if oldestUnix == cursor.BeforeUnix {
		cursor.Page++
	} else {
		cursor.BeforeUnix = oldestUnix
		cursor.Page = 1
	}

	if payload.AfterUnix > 0 && cursor.BeforeUnix <= payload.AfterUnix {
		cursorJSON, _ := json.Marshal(cursor)
		return r.Store.MarkJobCompleted(ctx, job.ID, string(cursorJSON))
	}

	cursorJSON, _ := json.Marshal(cursor)
	return r.Store.MarkJobQueued(ctx, job.ID, string(cursorJSON), time.Now().Add(2*time.Second))
}

func (r *Runner) handleSyncLatest(ctx context.Context, job storage.Job) error {
	if r.Ingestor == nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "ingestor not configured")
	}
	count, err := r.Ingestor.SyncLatestActivity(ctx)
	if err != nil {
		return r.markJobRetry(ctx, job, SyncSinceCursor{}, err)
	}
	cursor := SyncLatestCursor{Enqueued: count}
	cursorJSON, _ := json.Marshal(cursor)
	return r.Store.MarkJobCompleted(ctx, job.ID, string(cursorJSON))
}

func (r *Runner) markJobRetry(ctx context.Context, job storage.Job, cursor SyncSinceCursor, err error) error {
	cursorJSON, _ := json.Marshal(cursor)
	delay := retryDelay(job.Attempts)
	if strava.IsRateLimited(err) {
		if retryAfter, ok := strava.RateLimitBackoff(err); ok && retryAfter > 0 {
			delay = retryAfter
		} else if delay < 5*time.Minute {
			delay = 5 * time.Minute
		}
	}
	nextRun := time.Now().Add(delay)
	return r.Store.MarkJobRetry(ctx, job.ID, string(cursorJSON), err.Error(), nextRun)
}

func parseSyncSincePayload(raw string) (SyncSincePayload, error) {
	if raw == "" {
		return SyncSincePayload{}, fmt.Errorf("empty payload")
	}
	var payload SyncSincePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SyncSincePayload{}, err
	}
	return payload, nil
}

func parseSyncSinceCursor(raw string) (SyncSinceCursor, error) {
	if raw == "" {
		return SyncSinceCursor{Page: 1}, nil
	}
	var cursor SyncSinceCursor
	if err := json.Unmarshal([]byte(raw), &cursor); err != nil {
		return SyncSinceCursor{}, err
	}
	return cursor, nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		return 30 * time.Second
	}
	delay := 30 * time.Second
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return delay
}

func (r *Runner) staleAfter() time.Duration {
	if r.StaleAfter > 0 {
		return r.StaleAfter
	}
	return 10 * time.Minute
}
