package web

import (
	"strings"
	"testing"
	"time"

	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func TestBuildPrioritizedWeirdStatsLineUsesHistoryToSortFacts(t *testing.T) {
	snapshot := stats.StopStats{
		StopCount:             3,
		StopTotalSeconds:      95,
		TrafficLightStopCount: 2,
		RoadCrossingCount:     2,
	}
	rideFact := rideSegmentFact{
		DistanceMeters: 20000,
		AvgPower:       200,
		AvgSpeedMPS:    30.0 / 3.6,
	}
	coffeeFact := coffeeStopFact{Name: "Bean Machine"}
	routeFact := routeHighlightFact{Names: []string{"Victory Column", "Memorial Church"}}
	roadFact := roadCrossingFact{Count: 2, Roads: []string{"Unter den Linden", "Friedrichstrasse"}}

	histories := map[string]storage.UserFactMetricHistory{
		weirdStatsFactLongestSegment + ":" + factMetricDistanceMeters: {
			FactID:           weirdStatsFactLongestSegment,
			MetricID:         factMetricDistanceMeters,
			AllTimeSeenCount: 6,
			AllTimeBestValue: 60000,
			YearSeenCount:    6,
			YearBestValue:    60000,
		},
		weirdStatsFactCoffeeStop + ":" + factMetricPOIPrefix + "bean machine": {
			FactID:           weirdStatsFactCoffeeStop,
			MetricID:         factMetricPOIPrefix + "bean machine",
			AllTimeSeenCount: 2,
			AllTimeBestValue: 1,
			YearSeenCount:    2,
			YearBestValue:    1,
		},
		weirdStatsFactRouteHighlights + ":" + factMetricPOIPrefix + "victory column": {
			FactID:           weirdStatsFactRouteHighlights,
			MetricID:         factMetricPOIPrefix + "victory column",
			AllTimeSeenCount: 3,
			AllTimeBestValue: 1,
			YearSeenCount:    3,
			YearBestValue:    1,
		},
		weirdStatsFactRoadCrossings + ":" + factMetricCount: {
			FactID:           weirdStatsFactRoadCrossings,
			MetricID:         factMetricCount,
			AllTimeSeenCount: 4,
			AllTimeBestValue: 1,
			YearSeenCount:    4,
			YearBestValue:    1,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopCount: {
			FactID:           weirdStatsFactStopSummary,
			MetricID:         factMetricStopCount,
			AllTimeSeenCount: 5,
			AllTimeBestValue: 9,
			YearSeenCount:    5,
			YearBestValue:    9,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopTotal: {
			FactID:           weirdStatsFactStopSummary,
			MetricID:         factMetricStopTotal,
			AllTimeSeenCount: 5,
			AllTimeBestValue: 900,
			YearSeenCount:    5,
			YearBestValue:    900,
		},
		weirdStatsFactTrafficLightStops + ":" + factMetricCount: {
			FactID:           weirdStatsFactTrafficLightStops,
			MetricID:         factMetricCount,
			AllTimeSeenCount: 5,
			AllTimeBestValue: 5,
			YearSeenCount:    5,
			YearBestValue:    5,
		},
	}

	line := buildPrioritizedWeirdStatsLine(snapshot, rideFact, nil, coffeeFact, routeFact, roadFact, histories)
	want := "2 road crossings: Unter den Linden, Friedrichstrasse · Route highlights: Victory Column, Memorial Church · Detected Coffee Stop: Bean Machine · Longest uninterrupted segment: 20km - 200w - 30kmh"
	if line != want {
		t.Fatalf("unexpected prioritized line\nwant: %q\n got: %q", want, line)
	}
	if strings.Contains(line, "stops") || strings.Contains(line, "at lights") {
		t.Fatalf("expected lower-priority stop facts to be dropped, got %q", line)
	}
}

func TestBuildActivityFactMetricsIncludesPOIHistoryKeys(t *testing.T) {
	snapshot := stats.StopStats{
		StopCount:             2,
		StopTotalSeconds:      42,
		TrafficLightStopCount: 1,
		RoadCrossingCount:     2,
	}
	metrics := buildActivityFactMetrics(
		snapshot,
		rideSegmentFact{DistanceMeters: 48000, AvgPower: 200, AvgSpeedMPS: 30.0 / 3.6},
		[]speedMilestoneFact{{
			FactID:   weirdStatsFactAcceleration040,
			Label:    "0 to 40 km/h",
			StartKPH: 0,
			EndKPH:   40,
			Duration: 4 * time.Second,
		}},
		coffeeStopFact{Name: " Bean Machine "},
		routeHighlightFact{Names: []string{"Victory Column", "victory   column", "Memorial Church"}},
		roadCrossingFact{},
	)

	seen := make(map[string]bool, len(metrics))
	for _, metric := range metrics {
		seen[metric.FactID+":"+metric.MetricID] = true
	}

	for _, want := range []string{
		weirdStatsFactLongestSegment + ":" + factMetricDistanceMeters,
		weirdStatsFactAcceleration040 + ":" + factMetricInverseSeconds,
		weirdStatsFactCoffeeStop + ":" + factMetricPOIPrefix + "bean machine",
		weirdStatsFactRouteHighlights + ":" + factMetricPOIPrefix + "victory column",
		weirdStatsFactRouteHighlights + ":" + factMetricPOIPrefix + "memorial church",
		weirdStatsFactRoadCrossings + ":" + factMetricCount,
		weirdStatsFactStopSummary + ":" + factMetricStopCount,
		weirdStatsFactStopSummary + ":" + factMetricStopTotal,
		weirdStatsFactTrafficLightStops + ":" + factMetricCount,
	} {
		if !seen[want] {
			t.Fatalf("missing metric %q in %+v", want, metrics)
		}
	}
}

func TestBuildPrioritizedWeirdStatsLineBoostsAllTimeAndYearBestSpeedFacts(t *testing.T) {
	snapshot := stats.StopStats{
		StopCount:         3,
		StopTotalSeconds:  95,
		RoadCrossingCount: 2,
	}
	speedFacts := []speedMilestoneFact{
		{
			FactID:   weirdStatsFactAcceleration040,
			Label:    "0 to 40 km/h",
			StartKPH: 0,
			EndKPH:   40,
			Duration: 4 * time.Second,
		},
		{
			FactID:   weirdStatsFactDeceleration300,
			Label:    "30 to 0 km/h",
			StartKPH: 30,
			EndKPH:   0,
			Duration: 3 * time.Second,
		},
	}
	histories := map[string]storage.UserFactMetricHistory{
		weirdStatsFactAcceleration040 + ":" + factMetricInverseSeconds: {
			FactID:           weirdStatsFactAcceleration040,
			MetricID:         factMetricInverseSeconds,
			AllTimeSeenCount: 5,
			AllTimeBestValue: 0.20,
			YearSeenCount:    2,
			YearBestValue:    0.20,
		},
		weirdStatsFactDeceleration300 + ":" + factMetricInverseSeconds: {
			FactID:           weirdStatsFactDeceleration300,
			MetricID:         factMetricInverseSeconds,
			AllTimeSeenCount: 8,
			AllTimeBestValue: 0.50,
			YearSeenCount:    3,
			YearBestValue:    0.25,
		},
		weirdStatsFactRoadCrossings + ":" + factMetricCount: {
			FactID:           weirdStatsFactRoadCrossings,
			MetricID:         factMetricCount,
			AllTimeSeenCount: 5,
			AllTimeBestValue: 4,
			YearSeenCount:    2,
			YearBestValue:    3,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopCount: {
			FactID:           weirdStatsFactStopSummary,
			MetricID:         factMetricStopCount,
			AllTimeSeenCount: 5,
			AllTimeBestValue: 8,
			YearSeenCount:    2,
			YearBestValue:    6,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopTotal: {
			FactID:           weirdStatsFactStopSummary,
			MetricID:         factMetricStopTotal,
			AllTimeSeenCount: 5,
			AllTimeBestValue: 900,
			YearSeenCount:    2,
			YearBestValue:    600,
		},
	}

	line := buildPrioritizedWeirdStatsLine(snapshot, rideSegmentFact{}, speedFacts, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{Count: 2}, histories)
	want := "0-40kmh in 4s · 30-0kmh in 3s · 2 road crossings · 3 stops (1m 35s total)"
	if line != want {
		t.Fatalf("unexpected prioritized speed line\nwant: %q\n got: %q", want, line)
	}
}
