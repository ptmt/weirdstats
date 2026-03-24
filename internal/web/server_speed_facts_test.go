package web

import (
	"math"
	"testing"
	"time"

	"weirdstats/internal/gps"
)

func TestDetectSpeedMilestoneFacts(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 0},
		{Lat: 52.5201, Lon: 13.4041, Time: start.Add(1 * time.Second), Speed: 1},
		{Lat: 52.5202, Lon: 13.4042, Time: start.Add(3 * time.Second), Speed: 30.0 / 3.6},
		{Lat: 52.5203, Lon: 13.4043, Time: start.Add(5 * time.Second), Speed: 40.0 / 3.6},
		{Lat: 52.5204, Lon: 13.4044, Time: start.Add(6 * time.Second), Speed: 10},
		{Lat: 52.5205, Lon: 13.4045, Time: start.Add(7 * time.Second), Speed: 30.0 / 3.6},
		{Lat: 52.5206, Lon: 13.4046, Time: start.Add(9 * time.Second), Speed: speedMilestoneStopThresholdMPS},
	}

	facts := detectSpeedMilestoneFacts("Ride", points)
	if len(facts) != 4 {
		t.Fatalf("expected 4 speed facts, got %+v", facts)
	}

	byID := make(map[string]speedMilestoneFact, len(facts))
	for _, fact := range facts {
		byID[fact.FactID] = fact
	}

	assertDurationSeconds := func(factID string, want float64) {
		t.Helper()
		fact, ok := byID[factID]
		if !ok {
			t.Fatalf("missing speed fact %q in %+v", factID, facts)
		}
		if math.Abs(fact.Duration.Seconds()-want) > 0.01 {
			t.Fatalf("unexpected duration for %s: want %.2fs got %.2fs", factID, want, fact.Duration.Seconds())
		}
		if fact.StartIndex < 0 || fact.EndIndex <= fact.StartIndex {
			t.Fatalf("expected route segment indexes for %s, got %+v", factID, fact)
		}
	}

	assertDurationSeconds(weirdStatsFactAcceleration030, 2.5)
	assertDurationSeconds(weirdStatsFactAcceleration040, 4.5)
	assertDurationSeconds(weirdStatsFactDeceleration400, 4.0)
	assertDurationSeconds(weirdStatsFactDeceleration300, 2.0)
}

func TestBuildActivityMapFactsIncludesSpeedMilestones(t *testing.T) {
	start := time.Date(2026, time.March, 24, 8, 0, 0, 0, time.UTC)
	points := []gps.Point{
		{Lat: 52.5200, Lon: 13.4040, Time: start, Speed: 0},
		{Lat: 52.5201, Lon: 13.4041, Time: start.Add(1 * time.Second), Speed: 1},
		{Lat: 52.5202, Lon: 13.4042, Time: start.Add(3 * time.Second), Speed: 30.0 / 3.6},
		{Lat: 52.5203, Lon: 13.4043, Time: start.Add(5 * time.Second), Speed: 40.0 / 3.6},
	}
	speedFacts := []speedMilestoneFact{{
		FactID:       weirdStatsFactAcceleration040,
		Label:        "0 to 40 km/h",
		StartKPH:     0,
		EndKPH:       40,
		Duration:     4500 * time.Millisecond,
		StartIndex:   0,
		EndIndex:     3,
		StartLat:     points[0].Lat,
		StartLon:     points[0].Lon,
		EndLat:       points[3].Lat,
		EndLon:       points[3].Lon,
		DefaultOrder: 1,
		Color:        "#14b8a6",
	}}

	facts := buildActivityMapFacts(nil, points, rideSegmentFact{}, speedFacts, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{})
	if len(facts) != 1 {
		t.Fatalf("expected only the speed fact, got %+v", facts)
	}
	if facts[0].ID != weirdStatsFactAcceleration040 {
		t.Fatalf("expected acceleration fact, got %+v", facts[0])
	}
	if len(facts[0].Path) != 4 || len(facts[0].Points) != 2 {
		t.Fatalf("expected segment path and endpoints for speed fact, got %+v", facts[0])
	}
}
