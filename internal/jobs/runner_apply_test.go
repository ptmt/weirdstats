package jobs

import (
	"context"
	"testing"

	"weirdstats/internal/storage"
)

type stubActivityApplier struct {
	appliedID int64
}

func (s *stubActivityApplier) Apply(_ context.Context, activityID int64) error {
	s.appliedID = activityID
	return nil
}

func TestRunnerHandleApplyActivityRules(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	if err := EnqueueApplyActivityRules(ctx, store, 42, 1); err != nil {
		t.Fatalf("enqueue apply activity: %v", err)
	}

	applier := &stubActivityApplier{}
	runner := &Runner{Store: store, Applier: applier}
	processed, err := runner.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("process next: %v", err)
	}
	if !processed {
		t.Fatalf("expected job to be processed")
	}
	if applier.appliedID != 42 {
		t.Fatalf("expected activity 42 to be applied, got %d", applier.appliedID)
	}

	jobs, err := store.ListJobsByType(ctx, JobTypeApplyActivityRules, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 apply job, got %d", len(jobs))
	}
	if jobs[0].Status != "completed" {
		t.Fatalf("expected completed job, got %q", jobs[0].Status)
	}
}
