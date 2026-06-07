package web

import (
	"strings"
	"testing"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func TestDetectHeartRateChangeFact_Rise(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := heartRateTestPoints(start, []float64{
		120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120,
		130, 130, 130, 140, 150, 160, 170, 180, 180,
	})

	fact := detectHeartRateChangeFact(points)
	if fact.Direction != heartRateChangeDirectionRise {
		t.Fatalf("expected rise fact, got %+v", fact)
	}
	if fact.StartBPM != 130 || fact.EndBPM != 180 {
		t.Fatalf("unexpected HR endpoints: %+v", fact)
	}
	if fact.Duration != 28*time.Second {
		t.Fatalf("unexpected duration: %s", fact.Duration)
	}
	if fact.RateBPMPerMinute < 90 {
		t.Fatalf("expected steep HR rate, got %.1f", fact.RateBPMPerMinute)
	}
}

func TestDetectHeartRateChangeFact_Drop(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := heartRateTestPoints(start, []float64{
		150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150,
		180, 180, 180, 170, 160, 150, 140, 130, 130,
	})

	fact := detectHeartRateChangeFact(points)
	if fact.Direction != heartRateChangeDirectionDrop {
		t.Fatalf("expected drop fact, got %+v", fact)
	}
	if fact.StartBPM != 180 || fact.EndBPM != 130 {
		t.Fatalf("unexpected HR endpoints: %+v", fact)
	}
}

func TestDetectHeartRateChangeFact_IgnoresSinglePointSpike(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := heartRateTestPoints(start, []float64{
		120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120,
		130, 130, 130, 130, 130, 130, 130, 180,
	})

	if fact := detectHeartRateChangeFact(points); fact.Duration > 0 {
		t.Fatalf("expected spike to be ignored, got %+v", fact)
	}
}

func TestDetectHeartRateChangeFact_RejectsLargeSampleGaps(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := heartRateTestPoints(start, []float64{
		120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120,
		130, 130, 130, 140, 150, 160, 170, 180, 180,
	})
	points[20].Time = points[20].Time.Add(8 * time.Second)

	if fact := detectHeartRateChangeFact(points); fact.Duration > 0 {
		t.Fatalf("expected gappy HR window to be ignored, got %+v", fact)
	}
}

func TestBuildActivityMapFactsIncludesHeartRateChange(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := heartRateTestPoints(start, []float64{
		120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120, 120,
		130, 130, 130, 140, 150, 160, 170, 180, 180,
	})
	heartRateFact := detectHeartRateChangeFact(points)

	facts := buildActivityMapFactsWithHeartRate(nil, points, rideSegmentFact{}, nil, heartRateFact, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{})
	if len(facts) != 1 {
		t.Fatalf("expected only HR fact, got %+v", facts)
	}
	if facts[0].ID != weirdStatsFactHeartRateChange || facts[0].Title != "HR rise" {
		t.Fatalf("unexpected HR fact view: %+v", facts[0])
	}
	if len(facts[0].Path) == 0 || len(facts[0].Points) != 2 {
		t.Fatalf("expected HR fact path and endpoints, got %+v", facts[0])
	}
}

func TestHeartRateChangeDoesNotAutoPostFirstOccurrence(t *testing.T) {
	heartRateFact := heartRateChangeFact{
		Direction:        heartRateChangeDirectionRise,
		StartBPM:         130,
		EndBPM:           180,
		DeltaBPM:         50,
		RateBPMPerMinute: 100,
		Duration:         30 * time.Second,
	}
	settings := defaultWeirdStatsFactSettings()

	line := buildStravaWeirdStatsLineWithHeartRate(stats.StopStats{}, rideSegmentFact{}, nil, heartRateFact, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{}, settings, nil)
	if strings.Contains(line, "HR rise") {
		t.Fatalf("expected first HR change to stay out of Strava line, got %q", line)
	}
}

func TestHeartRateChangePostsMeaningfulRecord(t *testing.T) {
	heartRateFact := heartRateChangeFact{
		Direction:        heartRateChangeDirectionRise,
		StartBPM:         130,
		EndBPM:           180,
		DeltaBPM:         50,
		RateBPMPerMinute: 100,
		Duration:         30 * time.Second,
	}
	settings := defaultWeirdStatsFactSettings()
	histories := map[string]storage.UserFactMetricHistory{
		weirdStatsFactHeartRateChange + ":" + factMetricHRRiseRate: {
			FactID:           weirdStatsFactHeartRateChange,
			MetricID:         factMetricHRRiseRate,
			AllTimeSeenCount: 3,
			AllTimeBestValue: 80,
			YearSeenCount:    3,
			YearBestValue:    80,
		},
	}

	line := buildStravaWeirdStatsLineWithHeartRate(stats.StopStats{}, rideSegmentFact{}, nil, heartRateFact, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{}, settings, histories)
	if !strings.Contains(line, "HR rise: 130-180bpm in 30s (100bpm/min)") {
		t.Fatalf("expected remarkable HR change in Strava line, got %q", line)
	}
}

func heartRateTestPoints(start time.Time, values []float64) []gps.Point {
	points := make([]gps.Point, 0, len(values))
	for idx, value := range values {
		points = append(points, gps.Point{
			Lat:          52.52 + float64(idx)*0.0001,
			Lon:          13.40 + float64(idx)*0.0001,
			Time:         start.Add(time.Duration(idx*4) * time.Second),
			Speed:        3,
			HeartRate:    value,
			HasHeartRate: true,
		})
	}
	return points
}
