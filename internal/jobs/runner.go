package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"time"

	"weirdstats/internal/ingest"
	"weirdstats/internal/maps"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

const (
	JobTypeSyncActivitiesSince = "sync_activities_since"
	JobTypeSyncLatest          = "sync_latest"
	JobTypeProcessActivity     = "process_activity"
	JobTypeApplyActivityRules  = "apply_activity_rules"
	JobTypeEnrichActivity      = "enrich_activity"
)

type SyncSincePayload struct {
	UserID     int64 `json:"user_id"`
	AfterUnix  int64 `json:"after_unix"`
	PerPage    int   `json:"per_page"`
	WindowDays int   `json:"window_days"`
}

type SyncSinceCursor struct {
	Page            int   `json:"page"`
	Enqueued        int   `json:"enqueued"`
	WindowStartUnix int64 `json:"window_start_unix"`
	WindowEndUnix   int64 `json:"window_end_unix"`
	MaxBeforeUnix   int64 `json:"max_before_unix"`
}

type SyncLatestPayload struct {
	UserID int64 `json:"user_id"`
}

type SyncLatestCursor struct {
	Enqueued int `json:"enqueued"`
}

type ProcessActivityPayload struct {
	Publish    bool  `json:"publish,omitempty"`
	ActivityID int64 `json:"activity_id"`
	UserID     int64 `json:"user_id,omitempty"`
}

type ActivityProcessor interface {
	Process(ctx context.Context, activityID int64) error
}

type ActivityRuleApplier interface {
	Apply(ctx context.Context, activityID int64) error
}

type Runner struct {
	Store        *storage.Store
	Ingestor     *ingest.Ingestor
	Processor    ActivityProcessor
	Enricher     ActivityProcessor
	Applier      ActivityRuleApplier
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

	ctx = storage.ContextWithJob(ctx, job.ID)
	ctx = maps.WithAccessCheck(ctx, func(checkCtx context.Context) error {
		if err := r.Store.CheckActivityContext(checkCtx); err != nil {
			return err
		}
		prefs, err := r.Store.ProcessingPreferences(checkCtx, job.UserID)
		if err != nil {
			return err
		}
		if !prefs.ExternalMaps {
			return storage.ErrProcessingDisabled
		}
		return nil
	})
	ctx = storage.ContextWithBackfill(ctx, job.ParentID != 0 || job.Type == JobTypeSyncActivitiesSince)
	if job.MaxAttempts > 0 && job.Attempts >= job.MaxAttempts {
		message := job.LastError
		if message == "" {
			message = "Processing stopped after repeated failures."
		}
		if err := r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, message); err != nil {
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
	case JobTypeProcessActivity:
		if err := r.handleProcessActivity(ctx, job); err != nil {
			return true, err
		}
	case JobTypeEnrichActivity:
		if err := r.handleEnrichActivity(ctx, job); err != nil {
			return true, err
		}
	case JobTypeApplyActivityRules:
		if err := r.handleApplyActivityRules(ctx, job); err != nil {
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

	perPage := payload.PerPage
	if perPage <= 0 {
		perPage = 100
	}
	if cursor.Page <= 0 {
		cursor.Page = 1
	}
	if cursor.MaxBeforeUnix <= 0 {
		cursor.MaxBeforeUnix = time.Now().Unix()
	}
	if payload.AfterUnix > 0 && payload.AfterUnix >= cursor.MaxBeforeUnix {
		cursorJSON, _ := json.Marshal(cursor)
		return r.Store.MarkJobCompleted(ctx, job.ID, string(cursorJSON))
	}
	if cursor.WindowStartUnix <= 0 {
		cursor.WindowStartUnix = payload.AfterUnix
	}
	windowDays := payload.WindowDays
	if windowDays <= 0 {
		windowDays = 36500
	}
	windowSeconds := int64(windowDays) * int64((24*time.Hour)/time.Second)
	if cursor.WindowEndUnix <= 0 {
		cursor.WindowEndUnix = cursor.WindowStartUnix + windowSeconds
	}
	if cursor.WindowEndUnix > cursor.MaxBeforeUnix {
		cursor.WindowEndUnix = cursor.MaxBeforeUnix
	}
	if cursor.WindowStartUnix >= cursor.MaxBeforeUnix {
		cursorJSON, _ := json.Marshal(cursor)
		return r.Store.MarkJobCompleted(ctx, job.ID, string(cursorJSON))
	}

	if r.Ingestor == nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "strava client not configured")
	}
	client, err := r.Ingestor.ClientForUser(ctx, payload.UserID)
	if err != nil {
		return r.markJobRetry(ctx, job, cursor, err)
	}

	after := time.Unix(cursor.WindowStartUnix, 0)
	before := time.Unix(cursor.WindowEndUnix, 0)
	activities, err := client.ListActivities(ctx, after, before, cursor.Page, perPage)
	if err != nil {
		return r.markJobRetry(ctx, job, cursor, err)
	}

	var children []storage.Job
	for _, activity := range activities {
		body, _ := json.Marshal(ProcessActivityPayload{ActivityID: activity.ID, UserID: payload.UserID})
		children = append(children, storage.Job{Type: JobTypeProcessActivity, UserID: payload.UserID, ActivityID: activity.ID, ParentID: job.ID, Payload: string(body), MaxAttempts: 10})
		cursor.Enqueued++
	}
	status := "queued"
	if len(activities) >= perPage {
		cursor.Page++
	} else {
		cursor.Page = 1
		cursor.WindowStartUnix = cursor.WindowEndUnix
		cursor.WindowEndUnix = cursor.WindowStartUnix + windowSeconds
		if cursor.WindowEndUnix > cursor.MaxBeforeUnix {
			cursor.WindowEndUnix = cursor.MaxBeforeUnix
		}
		if cursor.WindowStartUnix >= cursor.MaxBeforeUnix {
			status = "completed"
		}
	}
	cursorJSON, _ := json.Marshal(cursor)
	return r.Store.EnqueueSyncPage(ctx, job, children, string(cursorJSON), status, time.Now().Add(2*time.Second))
}

func (r *Runner) handleSyncLatest(ctx context.Context, job storage.Job) error {
	if r.Ingestor == nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "ingestor not configured")
	}
	payload, err := parseSyncLatestPayload(job.Payload)
	if err != nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, fmt.Sprintf("invalid payload: %v", err))
	}
	if payload.UserID == 0 {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "missing user id")
	}
	count, err := r.Ingestor.SyncLatestActivity(ctx, payload.UserID)
	if err != nil {
		return r.markJobRetry(ctx, job, SyncSinceCursor{}, err)
	}
	cursor := SyncLatestCursor{Enqueued: count}
	cursorJSON, _ := json.Marshal(cursor)
	return r.Store.MarkJobCompleted(ctx, job.ID, string(cursorJSON))
}

func (r *Runner) handleProcessActivity(ctx context.Context, job storage.Job) error {
	payload, err := parseProcessActivityPayload(job.Payload)
	if err != nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, fmt.Sprintf("invalid payload: %v", err))
	}
	if payload.ActivityID == 0 {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "missing activity id")
	}
	if r.Processor == nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "processor not configured")
	}
	if payload.UserID != 0 {
		ctx = ingest.ContextWithUserID(ctx, payload.UserID)
	}
	workCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := r.Processor.Process(workCtx, payload.ActivityID); err != nil {
		return r.markJobRetry(ctx, job, SyncSinceCursor{}, err)
	}
	prefs, err := r.Store.ProcessingPreferences(ctx, payload.UserID)
	if err != nil {
		return err
	}
	if r.Enricher != nil && prefs.ExternalMaps {
		if _, err := r.Store.CreateJob(ctx, storage.Job{Type: JobTypeEnrichActivity, UserID: payload.UserID, ActivityID: payload.ActivityID, ParentID: job.ParentID, Payload: job.Payload, MaxAttempts: 5}); err != nil {
			return err
		}
	} else if payload.Publish && prefs.AutoPublish {
		if err := EnqueueApplyActivityRules(ctx, r.Store, payload.ActivityID, payload.UserID, true); err != nil {
			return err
		}
	}
	return r.Store.MarkJobCompleted(ctx, job.ID, job.Cursor)
}

func (r *Runner) handleEnrichActivity(ctx context.Context, job storage.Job) error {
	payload, err := parseProcessActivityPayload(job.Payload)
	if err != nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "Invalid activity job.")
	}
	prefs, err := r.Store.ProcessingPreferences(ctx, payload.UserID)
	if err != nil {
		return err
	}
	if prefs.ExternalMaps && r.Enricher != nil {
		workCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := r.Enricher.Process(workCtx, payload.ActivityID); err != nil {
			return r.markJobRetry(ctx, job, SyncSinceCursor{}, err)
		}
	}
	if payload.Publish && prefs.AutoPublish {
		if err := EnqueueApplyActivityRules(ctx, r.Store, payload.ActivityID, payload.UserID, true); err != nil {
			return err
		}
	}
	return r.Store.MarkJobCompleted(ctx, job.ID, job.Cursor)
}

func (r *Runner) handleApplyActivityRules(ctx context.Context, job storage.Job) error {
	payload, err := parseProcessActivityPayload(job.Payload)
	if err != nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, fmt.Sprintf("invalid payload: %v", err))
	}
	if payload.ActivityID == 0 {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "missing activity id")
	}
	if r.Applier == nil {
		return r.Store.MarkJobFailed(ctx, job.ID, job.Cursor, "applier not configured")
	}
	if payload.UserID != 0 {
		ctx = ingest.ContextWithUserID(ctx, payload.UserID)
	}
	ctx = storage.ContextWithAutomaticPublish(ctx, payload.Publish)
	if payload.Publish {
		prefs, err := r.Store.ProcessingPreferences(ctx, payload.UserID)
		if err != nil {
			return err
		}
		if !prefs.AutoPublish {
			return r.Store.MarkJobCompleted(ctx, job.ID, job.Cursor)
		}
	}
	workCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := r.Applier.Apply(workCtx, payload.ActivityID); err != nil {
		return r.markJobRetry(ctx, job, SyncSinceCursor{}, err)
	}
	return r.Store.MarkJobCompleted(ctx, job.ID, job.Cursor)
}

func (r *Runner) markJobRetry(ctx context.Context, job storage.Job, cursor SyncSinceCursor, err error) error {
	if errors.Is(err, storage.ErrProcessingDisabled) {
		return r.Store.MarkJobCompleted(ctx, job.ID, job.Cursor)
	}
	cursorJSON, _ := json.Marshal(cursor)
	if job.Type != JobTypeSyncActivitiesSince {
		cursorJSON = []byte(job.Cursor)
	}
	code, message := strava.SafeError(err)
	if job.Type == JobTypeEnrichActivity {
		code, message = "map_unavailable", "Imported and analyzed. Map details could not be completed and can be retried."
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrConnectionChanged) {
		code, message = "authorization_required", "Reconnect Strava to continue."
	}
	var apiCause *strava.APIError
	if errors.As(err, &apiCause) {
		log.Printf("job id=%d user_id=%d type=%s error_code=%s attempt=%d http_status=%d request_id=%q read_short=%d/%d read_daily=%d/%d", job.ID, job.UserID, job.Type, code, job.Attempts+1, apiCause.StatusCode, apiCause.RequestID, apiCause.RateLimit.ReadUsageShort, apiCause.RateLimit.ReadLimitShort, apiCause.RateLimit.ReadUsageLong, apiCause.RateLimit.ReadLimitLong)
	} else {
		log.Printf("job id=%d user_id=%d type=%s error_code=%s attempt=%d", job.ID, job.UserID, job.Type, code, job.Attempts+1)
	}
	if code == "authorization_required" || code == "read_permission_required" || code == "write_permission_required" {
		return r.Store.DeferJob(ctx, job.ID, string(cursorJSON), "blocked", code, message, time.Time{})
	}
	if code == "activity_unavailable" || code == "request_rejected" {
		return r.Store.DeferJob(ctx, job.ID, string(cursorJSON), "failed", code, message, time.Time{})
	}
	attempts := job.Attempts + 1
	delay := retryDelay(attempts)
	if strava.IsRateLimited(err) {
		if retryAfter, ok := strava.RateLimitBackoff(err); ok && retryAfter > 0 {
			delay = retryAfter
		} else {
			delay = time.Until(time.Now().UTC().Truncate(15 * time.Minute).Add(15*time.Minute + time.Second))
		}
		until := time.Now().Add(delay)
		bucket := "strava"
		var apiErr *strava.APIError
		if errors.As(err, &apiErr) && apiErr.QuotaBucket == "strava_backfill" {
			bucket = apiErr.QuotaBucket
		}
		if err := r.Store.SetAPICooldown(ctx, bucket, until); err != nil {
			return err
		}
		return r.Store.DeferJob(ctx, job.ID, string(cursorJSON), "waiting", code, message, until)
	}
	if err := r.Store.SetJobErrorCode(ctx, job.ID, code); err != nil {
		return err
	}
	if job.MaxAttempts > 0 && attempts >= job.MaxAttempts {
		return r.Store.MarkJobFailed(ctx, job.ID, string(cursorJSON), message)
	}
	delay += time.Duration(rand.Int63n(int64(delay)/5 + 1))
	nextRun := time.Now().Add(delay)
	return r.Store.MarkJobRetry(ctx, job.ID, string(cursorJSON), message, nextRun)
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

func parseProcessActivityPayload(raw string) (ProcessActivityPayload, error) {
	if raw == "" {
		return ProcessActivityPayload{}, fmt.Errorf("empty payload")
	}
	var payload ProcessActivityPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ProcessActivityPayload{}, err
	}
	return payload, nil
}

func parseSyncLatestPayload(raw string) (SyncLatestPayload, error) {
	if raw == "" {
		return SyncLatestPayload{}, fmt.Errorf("empty payload")
	}
	var payload SyncLatestPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SyncLatestPayload{}, err
	}
	return payload, nil
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
