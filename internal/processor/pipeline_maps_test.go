package processor

import (
	"context"
	"errors"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
	"weirdstats/internal/storage"
)

type unavailableMap struct{ calls int }

func (m *unavailableMap) NearbyFeatures(float64, float64) ([]maps.Feature, error) {
	m.calls++
	return nil, errors.New("maps unavailable")
}

// This is an existing-behavior guard: the production SeparateStages path must
// save local statistics without even entering a delayed/failing map lookup.
func TestSeparateStagesSavesStatsWithoutCallingUnavailableMaps(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1700000000, 0)
	points := []gps.Point{{Lat: 1, Lon: 1, Time: start, Speed: 0}, {Lat: 1, Lon: 1, Time: start.Add(10 * time.Second), Speed: 0}, {Lat: 1.01, Lon: 1.01, Time: start.Add(11 * time.Second), Speed: 4}}
	if _, err = store.InsertActivity(ctx, storage.Activity{ID: 42, UserID: 11, Type: "Ride", Name: "fixture", StartTime: start, MovingTime: 11}, points); err != nil {
		t.Fatal(err)
	}
	mapAPI := &unavailableMap{}
	p := PipelineProcessor{SeparateStages: true, Stats: &StopStatsProcessor{Store: store, MapAPI: mapAPI, Options: gps.StopOptions{SpeedThreshold: 0.5, MinDuration: 3 * time.Second}}}
	if err = p.Process(ctx, 42); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.GetActivityStats(ctx, 42)
	if err != nil || snapshot.StopCount != 1 {
		t.Fatalf("local stats not saved: %+v %v", snapshot, err)
	}
	if mapAPI.calls != 0 {
		t.Fatal("optional maps called during local analysis")
	}
	feed, err := store.ListActivitiesWithStats(ctx, 11, 10)
	if err != nil || len(feed) != 1 || !feed[0].HasStats {
		t.Fatalf("local activity hidden: %+v %v", feed, err)
	}
	routes, err := store.ListActivityRoutePreviewPoints(ctx, []int64{42}, 48)
	if err != nil || len(routes[42]) < 2 {
		t.Fatalf("GPS preview lost: %v", err)
	}
}
