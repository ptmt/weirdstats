package processor

import (
	"context"
	"testing"
)

type stubPipelineApplier struct {
	calls          int
	lastActivityID int64
}

func (s *stubPipelineApplier) Apply(_ context.Context, activityID int64) error {
	s.calls++
	s.lastActivityID = activityID
	return nil
}

func TestPipelineProcessorUsesApplierWhenConfigured(t *testing.T) {
	applier := &stubPipelineApplier{}
	pipeline := &PipelineProcessor{
		Applier: applier,
		Rules:   &RulesProcessor{},
	}

	if err := pipeline.Process(context.Background(), 42); err != nil {
		t.Fatalf("process: %v", err)
	}
	if applier.calls != 1 {
		t.Fatalf("expected applier to be called once, got %d", applier.calls)
	}
	if applier.lastActivityID != 42 {
		t.Fatalf("expected activity id 42, got %d", applier.lastActivityID)
	}
}
