package processor

import (
	"context"
	"math"
	"testing"
	"time"

	"weirdstats/internal/storage"
)

func TestComputeEffortWithHeartRateReference(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	base := time.Date(2024, 1, 1, 6, 0, 0, 0, time.UTC)
	priorRates := []float64{100, 140, 160}
	for i, hr := range priorRates {
		_, err := store.InsertActivity(ctx, storage.Activity{
			UserID:           1,
			Type:             "Run",
			Name:             "Baseline",
			StartTime:        base.Add(time.Duration(i) * time.Hour),
			Description:      "",
			MovingTime:       1800,
			AverageHeartRate: hr,
		}, nil)
		if err != nil {
			t.Fatalf("insert prior activity: %v", err)
		}
	}

	target := storage.Activity{
		UserID:           1,
		Type:             "Run",
		Name:             "Target",
		StartTime:        base.Add(4 * time.Hour),
		MovingTime:       3600,
		AverageHeartRate: 140,
	}
	score, version, err := computeEffort(ctx, store, target)
	if err != nil {
		t.Fatalf("compute effort: %v", err)
	}
	if version != effortVersion {
		t.Fatalf("expected version %d, got %d", effortVersion, version)
	}
	if math.Abs(score-120) > 0.0001 {
		t.Fatalf("expected effort 120, got %.4f", score)
	}
}
