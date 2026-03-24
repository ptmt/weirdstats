package web

import (
	"testing"

	"weirdstats/internal/storage"
)

func TestApplyDetectedFactRecordBadges(t *testing.T) {
	facts := []ActivityMapFactView{
		{ID: weirdStatsFactLongestSegment, Title: "Longest uninterrupted segment"},
		{ID: weirdStatsFactStopSummary, Title: "Stop summary"},
		{ID: weirdStatsFactRouteHighlights, Title: "Route highlights"},
	}
	metrics := []storage.ActivityFactMetric{
		{FactID: weirdStatsFactLongestSegment, MetricID: factMetricDistanceMeters, MetricValue: 1200},
		{FactID: weirdStatsFactStopSummary, MetricID: factMetricStopCount, MetricValue: 3},
		{FactID: weirdStatsFactStopSummary, MetricID: factMetricStopTotal, MetricValue: 95},
		{FactID: weirdStatsFactRouteHighlights, MetricID: factMetricPOIPrefix + "victory column", MetricValue: 1},
	}
	histories := map[string]storage.UserFactMetricHistory{
		weirdStatsFactLongestSegment + ":" + factMetricDistanceMeters: {
			FactID:           weirdStatsFactLongestSegment,
			MetricID:         factMetricDistanceMeters,
			AllTimeSeenCount: 4,
			AllTimeBestValue: 1800,
			YearSeenCount:    2,
			YearBestValue:    1200,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopCount: {
			FactID:           weirdStatsFactStopSummary,
			MetricID:         factMetricStopCount,
			AllTimeSeenCount: 4,
			AllTimeBestValue: 3,
			YearSeenCount:    2,
			YearBestValue:    3,
		},
		weirdStatsFactStopSummary + ":" + factMetricStopTotal: {
			FactID:           weirdStatsFactStopSummary,
			MetricID:         factMetricStopTotal,
			AllTimeSeenCount: 4,
			AllTimeBestValue: 95,
			YearSeenCount:    2,
			YearBestValue:    95,
		},
		weirdStatsFactRouteHighlights + ":" + factMetricPOIPrefix + "victory column": {
			FactID:           weirdStatsFactRouteHighlights,
			MetricID:         factMetricPOIPrefix + "victory column",
			AllTimeSeenCount: 2,
			AllTimeBestValue: 1,
			YearSeenCount:    1,
			YearBestValue:    1,
		},
	}

	got := applyDetectedFactRecordBadges(facts, metrics, histories, 2026)
	if got[0].BadgeLabel != "2026 best" || got[0].BadgeTone != "year" {
		t.Fatalf("expected year-best badge on longest segment, got %+v", got[0])
	}
	if got[1].BadgeLabel != "All-time best" || got[1].BadgeTone != "record" {
		t.Fatalf("expected all-time badge on stop summary, got %+v", got[1])
	}
	if got[2].BadgeLabel != "" || got[2].BadgeTone != "" {
		t.Fatalf("expected no record badge on route highlights, got %+v", got[2])
	}
}
