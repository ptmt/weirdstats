package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"weirdstats/internal/stats"
)

func TestSyncSeparatesImportedActivitiesFromOptionalStageFailures(t *testing.T) {
	ctx := context.Background()
	s := syncTestStore(t)
	for _, id := range []int64{1193, 1214, 1218, 1238, 1256, 1262, 1264} {
		if _, err := s.UpsertActivity(ctx, Activity{ID: id, UserID: 11, Type: "Ride", Name: "Synthetic ride", StartTime: time.Now(), StreamsFetched: true}, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertActivityStats(ctx, id, stats.StopStats{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateJob(ctx, Job{Type: "enrich_activity", UserID: 11, ActivityID: id, Status: "failed", ErrorCode: "map_unavailable"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateJob(ctx, Job{Type: "apply_activity_rules", UserID: 11, ActivityID: 1193, Status: "blocked", ErrorCode: "write_permission_required"}); err != nil {
		t.Fatal(err)
	}
	status, err := s.UserSyncStatus(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(status)
	var fields map[string]int
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if status.Completed != 7 || status.Failed != 0 || status.Blocked != 0 || fields["map_failed"] != 7 || fields["publishing_blocked"] != 1 {
		t.Fatalf("optional failures misreported as failed imports: %s", wire)
	}
	if _, err := s.CreateJob(ctx, Job{Type: "process_activity", UserID: 11, ActivityID: 9999, Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateJob(ctx, Job{Type: "sync_latest", UserID: 11, Status: "blocked"}); err != nil {
		t.Fatal(err)
	}
	status, err = s.UserSyncStatus(ctx, 11)
	if err != nil || status.Failed != 1 || status.Blocked != 1 {
		t.Fatalf("real import errors hidden: %+v %v", status, err)
	}
}
