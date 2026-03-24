package web

import (
	"strings"
	"testing"

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
			FactID:    weirdStatsFactLongestSegment,
			MetricID:  factMetricDistanceMeters,
			SeenCount: 6,
			BestValue: 60000,
		},
		weirdStatsFactCoffeeStop + ":" + factMetricPOIPrefix + "bean machine": {
			FactID:    weirdStatsFactCoffeeStop,
			MetricID:  factMetricPOIPrefix + "bean machine",
			SeenCount: 2,
			BestValue: 1,
		},
		weirdStatsFactRouteHighlights + ":" + factMetricPOIPrefix + "victory column": {
			FactID:    weirdStatsFactRouteHighlights,
			MetricID:  factMetricPOIPrefix + "victory column",
			SeenCount: 3,
			BestValue: 1,
		},
		weirdStatsFactRoadCrossings + ":" + factMetricCount: {
			FactID:    weirdStatsFactRoadCrossings,
			MetricID:  factMetricCount,
			SeenCount: 4,
			BestValue: 1,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopCount: {
			FactID:    weirdStatsFactStopSummary,
			MetricID:  factMetricStopCount,
			SeenCount: 5,
			BestValue: 9,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopTotal: {
			FactID:    weirdStatsFactStopSummary,
			MetricID:  factMetricStopTotal,
			SeenCount: 5,
			BestValue: 900,
		},
		weirdStatsFactTrafficLightStops + ":" + factMetricCount: {
			FactID:    weirdStatsFactTrafficLightStops,
			MetricID:  factMetricCount,
			SeenCount: 5,
			BestValue: 5,
		},
	}

	line := buildPrioritizedWeirdStatsLine(snapshot, rideFact, coffeeFact, routeFact, roadFact, histories)
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
