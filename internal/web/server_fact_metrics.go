package web

import (
	"fmt"

	"weirdstats/internal/storage"
)

const (
	factMetricDistanceMeters = "distance_meters"
	factMetricStopCount      = "stop_count"
	factMetricStopTotal      = "stop_total_seconds"
	factMetricCount          = "count"
)

func buildActivityFactMetrics(stopViews []StopView, rideFact rideSegmentFact, roadFact roadCrossingFact) []storage.ActivityFactMetric {
	metrics := make([]storage.ActivityFactMetric, 0, 5)

	if summary := buildRideSegmentPart(rideFact); summary != "" && rideFact.DistanceMeters > 0 {
		metrics = append(metrics, storage.ActivityFactMetric{
			FactID:      weirdStatsFactLongestSegment,
			MetricID:    factMetricDistanceMeters,
			MetricValue: rideFact.DistanceMeters,
			Summary:     summary,
		})
	}

	if len(stopViews) > 0 {
		summary := stopSummaryFactSummary(stopViews)
		metrics = append(metrics,
			storage.ActivityFactMetric{
				FactID:      weirdStatsFactStopSummary,
				MetricID:    factMetricStopCount,
				MetricValue: float64(len(stopViews)),
				Summary:     summary,
			},
			storage.ActivityFactMetric{
				FactID:      weirdStatsFactStopSummary,
				MetricID:    factMetricStopTotal,
				MetricValue: float64(totalStopSeconds(stopViews)),
				Summary:     summary,
			},
		)
	}

	lightStops := countLightStops(stopViews)
	if lightStops > 0 {
		metrics = append(metrics, storage.ActivityFactMetric{
			FactID:      weirdStatsFactTrafficLightStops,
			MetricID:    factMetricCount,
			MetricValue: float64(lightStops),
			Summary:     trafficLightStopsFactSummary(lightStops),
		})
	}

	if summary := buildRoadCrossingPart(roadFact); summary != "" && roadFact.Count > 0 {
		metrics = append(metrics, storage.ActivityFactMetric{
			FactID:      weirdStatsFactRoadCrossings,
			MetricID:    factMetricCount,
			MetricValue: float64(roadFact.Count),
			Summary:     summary,
		})
	}

	return metrics
}

func stopSummaryFactSummary(stopViews []StopView) string {
	summary := fmt.Sprintf("%d stops", len(stopViews))
	if total := totalStopSeconds(stopViews); total > 0 {
		summary += " · " + formatDuration(total) + " total"
	}
	return summary
}

func trafficLightStopsFactSummary(count int) string {
	return fmt.Sprintf("%d detected near traffic signals", count)
}
