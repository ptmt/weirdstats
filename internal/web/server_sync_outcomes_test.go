package web

import (
	"context"
	"strings"
	"testing"

	"weirdstats/internal/storage"
)

func TestMapFailuresDescribeAvailableImportsNotFailedActivities(t *testing.T) {
	s, store := newAutomationTestServer(t)
	ctx := context.Background()
	for id := int64(1); id <= 7; id++ {
		if _, err := store.CreateJob(ctx, storage.Job{Type: "enrich_activity", UserID: 11, ActivityID: id, Status: "failed", ErrorCode: "map_unavailable"}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.syncView(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if view.Failed != 0 || view.Label != "Map details unavailable" || !strings.Contains(view.Message, "Imported") || view.State != "attention" {
		t.Fatalf("misleading import status: %+v", view)
	}
	if len(view.Errors) != 1 || view.Errors[0].Count != 7 {
		t.Fatalf("lost diagnostics: %+v", view)
	}
}
