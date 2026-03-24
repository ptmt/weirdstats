package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

func TestBuildRoutePreviewPath(t *testing.T) {
	points := []storage.ActivityRoutePoint{
		{Lat: 37.7788, Lon: -122.4350},
		{Lat: 37.7762, Lon: -122.4269},
		{Lat: 37.7701, Lon: -122.4213},
		{Lat: 37.7685, Lon: -122.4120},
	}

	path, startX, startY, endX, endY, ok := buildRoutePreviewPath(points, 188, 62, 8)
	if !ok {
		t.Fatalf("expected route preview path")
	}
	if path == "" {
		t.Fatalf("expected non-empty svg path")
	}
	if !strings.HasPrefix(path, "M ") {
		t.Fatalf("expected path to start with move command, got %q", path)
	}
	if !strings.Contains(path, " L ") {
		t.Fatalf("expected line segments in path, got %q", path)
	}
	if startX == endX && startY == endY {
		t.Fatalf("expected distinct start/end points")
	}
	if startX < 0 || startX > 188 || endX < 0 || endX > 188 {
		t.Fatalf("x coordinates out of bounds: start=%f end=%f", startX, endX)
	}
	if startY < 0 || startY > 62 || endY < 0 || endY > 62 {
		t.Fatalf("y coordinates out of bounds: start=%f end=%f", startY, endY)
	}
}

func TestBuildRoutePreviewPathRejectsSinglePoint(t *testing.T) {
	_, _, _, _, _, ok := buildRoutePreviewPath([]storage.ActivityRoutePoint{{Lat: 1, Lon: 1}}, 188, 62, 8)
	if ok {
		t.Fatalf("expected single-point route to be rejected")
	}
}

func TestBuildContributionDataForYear_UsesMondayWeekStart(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	server := &Server{store: store}

	oldLocal := time.Local
	time.Local = time.UTC
	defer func() {
		time.Local = oldLocal
	}()

	data := server.buildContributionDataForYear(ctx, 1, 2026, time.Date(2027, time.January, 15, 0, 0, 0, 0, time.UTC))
	if len(data.Days) != data.Weeks*7 {
		t.Fatalf("unexpected grid size: got %d days for %d weeks", len(data.Days), data.Weeks)
	}

	wantFirstWeek := []string{
		"2025-12-29",
		"2025-12-30",
		"2025-12-31",
		"2026-01-01",
		"2026-01-02",
		"2026-01-03",
		"2026-01-04",
	}
	for i, want := range wantFirstWeek {
		if got := data.Days[i].Date; got != want {
			t.Fatalf("unexpected first week day at index %d: want %s got %s", i, want, got)
		}
	}

	if got := data.Days[len(data.Days)-1].Date; got != "2027-01-03" {
		t.Fatalf("unexpected last grid day: want 2027-01-03 got %s", got)
	}
}

func TestBuildStopDetectionDataItem_ExplainsShortPause(t *testing.T) {
	start := time.Date(2026, time.March, 23, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Time: start, Speed: 7},
		{Time: start.Add(1 * time.Minute), Speed: 0},
		{Time: start.Add(1*time.Minute + 45*time.Second), Speed: 0},
		{Time: start.Add(2 * time.Minute), Speed: 7},
	}

	item := buildStopDetectionDataItem(points, nil, true, gps.StopOptions{
		SpeedThreshold: 0.5,
		MinDuration:    time.Minute,
	})

	if item.Value != "0 stops" {
		t.Fatalf("unexpected value: %q", item.Value)
	}
	for _, want := range []string{
		"Stop processing completed.",
		"1 candidate low-speed window found",
		"45s",
		"1m 0s",
		"0.5 m/s",
	} {
		if !strings.Contains(item.Detail, want) {
			t.Fatalf("expected %q in detail %q", want, item.Detail)
		}
	}
	if item.Tone != "warning" {
		t.Fatalf("expected warning tone, got %q", item.Tone)
	}
}

func TestBuildEnrichmentDataItem_ExplainsUnavailableOverpass(t *testing.T) {
	item := buildEnrichmentDataItem(true, false)

	if item.Value != "overpass unavailable" {
		t.Fatalf("unexpected value: %q", item.Value)
	}
	for _, want := range []string{
		"coffee stops",
		"route highlights",
		"named road crossings",
		"3m 0s",
	} {
		if !strings.Contains(item.Detail, want) {
			t.Fatalf("expected %q in detail %q", want, item.Detail)
		}
	}
	if item.Tone != "warning" {
		t.Fatalf("expected warning tone, got %q", item.Tone)
	}
}

func TestBuildCoffeeStopDataItem_ExplainsNearMiss(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Time: start, Speed: 7},
		{Time: start.Add(1 * time.Minute), Speed: 0},
		{Time: start.Add(3*time.Minute + 30*time.Second), Speed: 0},
		{Time: start.Add(4 * time.Minute), Speed: 7},
	}

	item := buildCoffeeStopDataItem("Ride", nil, true, points, true)

	if item.Value != "near miss" {
		t.Fatalf("unexpected value: %q", item.Value)
	}
	for _, want := range []string{
		"1 low-speed pause found",
		"2m 30s",
		"3m 0s",
		"0.5 m/s",
	} {
		if !strings.Contains(item.Detail, want) {
			t.Fatalf("expected %q in detail %q", want, item.Detail)
		}
	}
	if item.Tone != "warning" {
		t.Fatalf("expected warning tone, got %q", item.Tone)
	}
}

func TestBuildCoffeeStopDataItem_ExplainsNoNearbyCafe(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Time: start, Speed: 7},
		{Time: start.Add(1 * time.Minute), Speed: 0},
		{Time: start.Add(4 * time.Minute), Speed: 0},
		{Time: start.Add(5 * time.Minute), Speed: 7},
	}

	item := buildCoffeeStopDataItem("Ride", nil, true, points, true)

	if item.Value != "not found" {
		t.Fatalf("unexpected value: %q", item.Value)
	}
	for _, want := range []string{
		"1 qualifying pause found",
		"3m 0s",
		"45m",
	} {
		if !strings.Contains(item.Detail, want) {
			t.Fatalf("expected %q in detail %q", want, item.Detail)
		}
	}
	if item.Tone != "warning" {
		t.Fatalf("expected warning tone, got %q", item.Tone)
	}
}
