package storage

import (
	"context"
	"testing"
	"time"
)

func TestReplaceActivityFactMetricsTracksPerUserYearRecords(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	startA := time.Date(2026, time.January, 10, 8, 0, 0, 0, time.UTC)
	startB := time.Date(2026, time.February, 10, 8, 0, 0, 0, time.UTC)
	startPrev := time.Date(2025, time.December, 15, 8, 0, 0, 0, time.UTC)
	startOtherUser := time.Date(2026, time.March, 5, 8, 0, 0, 0, time.UTC)

	activityA, err := store.InsertActivity(ctx, Activity{UserID: 1, Type: "Ride", Name: "A", StartTime: startA}, nil)
	if err != nil {
		t.Fatalf("insert activity A: %v", err)
	}
	activityB, err := store.InsertActivity(ctx, Activity{UserID: 1, Type: "Ride", Name: "B", StartTime: startB}, nil)
	if err != nil {
		t.Fatalf("insert activity B: %v", err)
	}
	activityPrev, err := store.InsertActivity(ctx, Activity{UserID: 1, Type: "Ride", Name: "Prev", StartTime: startPrev}, nil)
	if err != nil {
		t.Fatalf("insert previous-year activity: %v", err)
	}
	activityOtherUser, err := store.InsertActivity(ctx, Activity{UserID: 2, Type: "Ride", Name: "Other", StartTime: startOtherUser}, nil)
	if err != nil {
		t.Fatalf("insert other-user activity: %v", err)
	}

	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityA, UserID: 1, StartTime: startA}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 1000, Summary: "A longest"},
		{FactID: "stop_summary", MetricID: "stop_count", MetricValue: 2, Summary: "A stops"},
		{FactID: "stop_summary", MetricID: "stop_total_seconds", MetricValue: 600, Summary: "A stops"},
	}); err != nil {
		t.Fatalf("replace metrics A: %v", err)
	}
	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityB, UserID: 1, StartTime: startB}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 2000, Summary: "B longest"},
		{FactID: "stop_summary", MetricID: "stop_count", MetricValue: 1, Summary: "B stops"},
		{FactID: "stop_summary", MetricID: "stop_total_seconds", MetricValue: 300, Summary: "B stops"},
	}); err != nil {
		t.Fatalf("replace metrics B: %v", err)
	}
	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityPrev, UserID: 1, StartTime: startPrev}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 5000, Summary: "Previous year"},
	}); err != nil {
		t.Fatalf("replace previous-year metrics: %v", err)
	}
	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityOtherUser, UserID: 2, StartTime: startOtherUser}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 9000, Summary: "Other user"},
	}); err != nil {
		t.Fatalf("replace other-user metrics: %v", err)
	}

	assertRecord := func(records []UserYearFactRecord, key string, wantActivity int64, wantValue float64, wantSummary string) {
		t.Helper()
		byKey := make(map[string]UserYearFactRecord, len(records))
		for _, record := range records {
			byKey[record.FactID+":"+record.MetricID] = record
		}
		record, ok := byKey[key]
		if !ok {
			t.Fatalf("missing record %q in %+v", key, records)
		}
		if record.ActivityID != wantActivity {
			t.Fatalf("record %q activity: want %d got %d", key, wantActivity, record.ActivityID)
		}
		if record.Value != wantValue {
			t.Fatalf("record %q value: want %.0f got %.0f", key, wantValue, record.Value)
		}
		if record.Summary != wantSummary {
			t.Fatalf("record %q summary: want %q got %q", key, wantSummary, record.Summary)
		}
	}

	records, err := store.ListUserYearFactRecords(ctx, 1, 2026)
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	assertRecord(records, "longest_segment:distance_meters", activityB, 2000, "B longest")
	assertRecord(records, "stop_summary:stop_count", activityA, 2, "A stops")
	assertRecord(records, "stop_summary:stop_total_seconds", activityA, 600, "A stops")

	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityB, UserID: 1, StartTime: startB}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 800, Summary: "B shorter"},
		{FactID: "stop_summary", MetricID: "stop_count", MetricValue: 4, Summary: "B more stops"},
		{FactID: "stop_summary", MetricID: "stop_total_seconds", MetricValue: 1200, Summary: "B more stops"},
	}); err != nil {
		t.Fatalf("replace downgraded metrics B: %v", err)
	}

	records, err = store.ListUserYearFactRecords(ctx, 1, 2026)
	if err != nil {
		t.Fatalf("list updated records: %v", err)
	}
	assertRecord(records, "longest_segment:distance_meters", activityA, 1000, "A longest")
	assertRecord(records, "stop_summary:stop_count", activityB, 4, "B more stops")
	assertRecord(records, "stop_summary:stop_total_seconds", activityB, 1200, "B more stops")

	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityA, UserID: 1, StartTime: startA}, nil); err != nil {
		t.Fatalf("clear metrics A: %v", err)
	}

	records, err = store.ListUserYearFactRecords(ctx, 1, 2026)
	if err != nil {
		t.Fatalf("list records after clearing A: %v", err)
	}
	assertRecord(records, "longest_segment:distance_meters", activityB, 800, "B shorter")
}

func TestListUserFactMetricHistoriesExcludesCurrentActivity(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	startA := time.Date(2026, time.January, 10, 8, 0, 0, 0, time.UTC)
	startB := time.Date(2026, time.February, 10, 8, 0, 0, 0, time.UTC)
	startOtherUser := time.Date(2026, time.March, 5, 8, 0, 0, 0, time.UTC)

	activityA, err := store.InsertActivity(ctx, Activity{UserID: 1, Type: "Ride", Name: "A", StartTime: startA}, nil)
	if err != nil {
		t.Fatalf("insert activity A: %v", err)
	}
	activityB, err := store.InsertActivity(ctx, Activity{UserID: 1, Type: "Ride", Name: "B", StartTime: startB}, nil)
	if err != nil {
		t.Fatalf("insert activity B: %v", err)
	}
	activityOtherUser, err := store.InsertActivity(ctx, Activity{UserID: 2, Type: "Ride", Name: "Other", StartTime: startOtherUser}, nil)
	if err != nil {
		t.Fatalf("insert activity other user: %v", err)
	}

	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityA, UserID: 1, StartTime: startA}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 42000, Summary: "A longest"},
		{FactID: "route_highlights", MetricID: "poi:victory column", MetricValue: 1, Summary: "Victory Column"},
	}); err != nil {
		t.Fatalf("replace metrics A: %v", err)
	}
	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityB, UserID: 1, StartTime: startB}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 51000, Summary: "B longest"},
		{FactID: "route_highlights", MetricID: "poi:victory column", MetricValue: 1, Summary: "Victory Column"},
		{FactID: "route_highlights", MetricID: "poi:memorial church", MetricValue: 1, Summary: "Memorial Church"},
	}); err != nil {
		t.Fatalf("replace metrics B: %v", err)
	}
	if err := store.ReplaceActivityFactMetrics(ctx, Activity{ID: activityOtherUser, UserID: 2, StartTime: startOtherUser}, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters", MetricValue: 99999, Summary: "Other longest"},
	}); err != nil {
		t.Fatalf("replace metrics other user: %v", err)
	}

	histories, err := store.ListUserFactMetricHistories(ctx, 1, activityB, 2026, []ActivityFactMetric{
		{FactID: "longest_segment", MetricID: "distance_meters"},
		{FactID: "route_highlights", MetricID: "poi:victory column"},
		{FactID: "route_highlights", MetricID: "poi:memorial church"},
		{FactID: "route_highlights", MetricID: "poi:memorial church"},
	})
	if err != nil {
		t.Fatalf("list metric histories: %v", err)
	}

	longest := histories["longest_segment:distance_meters"]
	if longest.AllTimeSeenCount != 1 {
		t.Fatalf("expected 1 prior longest segment, got %+v", longest)
	}
	if longest.AllTimeBestValue != 42000 {
		t.Fatalf("expected longest best value 42000, got %+v", longest)
	}
	if longest.YearSeenCount != 1 || longest.YearBestValue != 42000 {
		t.Fatalf("expected 2026 longest best value 42000, got %+v", longest)
	}

	victory := histories["route_highlights:poi:victory column"]
	if victory.AllTimeSeenCount != 1 {
		t.Fatalf("expected 1 prior victory column record, got %+v", victory)
	}
	if victory.AllTimeBestValue != 1 {
		t.Fatalf("expected victory column best value 1, got %+v", victory)
	}
	if victory.YearSeenCount != 1 || victory.YearBestValue != 1 {
		t.Fatalf("expected 2026 victory column best value 1, got %+v", victory)
	}

	if _, ok := histories["route_highlights:poi:memorial church"]; ok {
		t.Fatalf("expected no prior memorial church record, got %+v", histories["route_highlights:poi:memorial church"])
	}
}
