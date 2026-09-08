package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

type failingProcessor struct {
	err   error
	calls int
}

func (p *failingProcessor) Process(context.Context, int64) error { p.calls++; return p.err }
func (p *failingProcessor) Apply(context.Context, int64) error   { p.calls++; return p.err }

func TestAutomaticPublishChecksCurrentConsent(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProcessingPreferences(ctx, 11, storage.ProcessingPreferences{AutoPublish: true}); err != nil {
		t.Fatal(err)
	}
	if err = EnqueueApplyActivityRules(ctx, store, 42, 11, true); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProcessingPreferences(ctx, 11, storage.ProcessingPreferences{}); err != nil {
		t.Fatal(err)
	}
	applier := &failingProcessor{}
	r := Runner{Store: store, Applier: applier}
	if _, err = r.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 0 {
		t.Fatal("published after consent withdrawn")
	}
	// A subsequent explicit request is independent of automatic publishing.
	if err = EnqueueApplyActivityRules(ctx, store, 42, 11); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 1 {
		t.Fatal("manual publish was blocked")
	}
}

func TestBackfillWaitDoesNotBlockInteractiveJobs(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateJob(ctx, storage.Job{Type: JobTypeProcessActivity, UserID: 11, ActivityID: 42, ParentID: 100, Payload: `{"user_id":11,"activity_id":42}`}); err != nil {
		t.Fatal(err)
	}
	p := &failingProcessor{err: &strava.APIError{StatusCode: 429, QuotaBucket: "strava_backfill", RateLimit: strava.RateLimitInfo{RetryAfter: time.Hour}}}
	r := Runner{Store: store, Processor: p, Applier: p}
	if _, err = r.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if err = EnqueueApplyActivityRules(ctx, store, 99, 22); err != nil {
		t.Fatal(err)
	}
	p.err = nil
	if worked, err := r.ProcessNext(ctx); err != nil || !worked {
		t.Fatalf("interactive job blocked: %v %v", worked, err)
	}
	if p.calls != 2 {
		t.Fatalf("calls=%d", p.calls)
	}
}

func TestRateLimitDoesNotConsumeAttemptsOrRunOtherUsers(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateJob(ctx, storage.Job{Type: JobTypeProcessActivity, Payload: `{"activity_id":1,"user_id":1}`, Attempts: 9, MaxAttempts: 10}); err != nil {
		t.Fatal(err)
	}
	if err = store.EnqueueActivity(ctx, 2, 2); err != nil {
		t.Fatal(err)
	}
	p := &failingProcessor{err: &strava.APIError{StatusCode: 429, RateLimit: strava.RateLimitInfo{RetryAfter: time.Hour}}}
	r := Runner{Store: store, Processor: p}
	if _, err = r.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if worked, err := r.ProcessNext(ctx); err != nil || worked {
		t.Fatalf("during wait worked=%v err=%v", worked, err)
	}
	rows, err := store.ListUserJobs(ctx, 1, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Attempts != 9 || rows[0].Status != "waiting" || p.calls != 1 {
		t.Fatalf("job=%+v calls=%d", rows[0], p.calls)
	}
}

func TestPermanentPermissionBlocksAndTransientExhaustionRetainsCause(t *testing.T) {
	for _, tc := range []struct {
		err          error
		code, status string
	}{
		{&strava.PermissionError{Scope: "activity:read"}, "read_permission_required", "blocked"},
		{errors.New("private coordinates and secret URL"), "processing_error", "failed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			ctx := context.Background()
			store, err := storage.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.InitSchema(ctx); err != nil {
				t.Fatal(err)
			}
			_, err = store.CreateJob(ctx, storage.Job{Type: JobTypeProcessActivity, Payload: `{"activity_id":1,"user_id":1}`, Attempts: 9, MaxAttempts: 10})
			if err != nil {
				t.Fatal(err)
			}
			r := Runner{Store: store, Processor: &failingProcessor{err: tc.err}}
			if _, err = r.ProcessNext(ctx); err != nil {
				t.Fatal(err)
			}
			rows, err := store.ListJobs(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if rows[0].Status != tc.status || rows[0].ErrorCode != tc.code || rows[0].LastError == "max attempts exceeded" || rows[0].LastError == tc.err.Error() {
				t.Fatalf("unsafe or missing error: %+v", rows[0])
			}
		})
	}
}
