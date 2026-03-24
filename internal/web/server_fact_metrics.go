package web

import (
	"fmt"
	"strings"

	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

const (
	factMetricDistanceMeters = "distance_meters"
	factMetricStopCount      = "stop_count"
	factMetricStopTotal      = "stop_total_seconds"
	factMetricCount          = "count"
	factMetricInverseSeconds = "inverse_seconds"
	factMetricPOIPrefix      = "poi:"
)

func buildActivityFactMetrics(
	statsSnapshot stats.StopStats,
	rideFact rideSegmentFact,
	speedFacts []speedMilestoneFact,
	coffeeFact coffeeStopFact,
	routeFact routeHighlightFact,
	roadFact roadCrossingFact,
) []storage.ActivityFactMetric {
	metrics := make([]storage.ActivityFactMetric, 0, 12)
	metrics = append(metrics, rideSegmentFactMetrics(rideFact)...)
	metrics = append(metrics, speedMilestoneFactMetrics(speedFacts)...)
	metrics = append(metrics, coffeeStopFactMetrics(coffeeFact)...)
	metrics = append(metrics, routeHighlightFactMetrics(routeFact)...)
	metrics = append(metrics, roadCrossingFactMetrics(statsSnapshot, roadFact)...)
	metrics = append(metrics, stopSummaryFactMetrics(statsSnapshot)...)
	metrics = append(metrics, trafficLightStopFactMetrics(statsSnapshot)...)
	return metrics
}

func rideSegmentFactMetrics(rideFact rideSegmentFact) []storage.ActivityFactMetric {
	if summary := buildRideSegmentPart(rideFact); summary != "" && rideFact.DistanceMeters > 0 {
		return []storage.ActivityFactMetric{{
			FactID:      weirdStatsFactLongestSegment,
			MetricID:    factMetricDistanceMeters,
			MetricValue: rideFact.DistanceMeters,
			Summary:     summary,
		}}
	}
	return nil
}

func speedMilestoneFactMetrics(facts []speedMilestoneFact) []storage.ActivityFactMetric {
	if len(facts) == 0 {
		return nil
	}

	metrics := make([]storage.ActivityFactMetric, 0, len(facts))
	for _, fact := range facts {
		if fact.Duration <= 0 {
			continue
		}
		seconds := fact.Duration.Seconds()
		if seconds <= 0 {
			continue
		}
		metrics = append(metrics, storage.ActivityFactMetric{
			FactID:      fact.FactID,
			MetricID:    factMetricInverseSeconds,
			MetricValue: 1 / seconds,
			Summary:     speedMilestoneSummary(fact),
		})
	}
	return metrics
}

func coffeeStopFactMetrics(fact coffeeStopFact) []storage.ActivityFactMetric {
	name := strings.TrimSpace(fact.Name)
	metricID := factMetricNameID(name)
	if metricID == "" {
		return nil
	}
	return []storage.ActivityFactMetric{{
		FactID:      weirdStatsFactCoffeeStop,
		MetricID:    metricID,
		MetricValue: 1,
		Summary:     name,
	}}
}

func routeHighlightFactMetrics(fact routeHighlightFact) []storage.ActivityFactMetric {
	names := uniqueFactMetricNames(fact.Names)
	if len(names) == 0 {
		return nil
	}

	metrics := make([]storage.ActivityFactMetric, 0, len(names))
	for _, name := range names {
		metricID := factMetricNameID(name)
		if metricID == "" {
			continue
		}
		metrics = append(metrics, storage.ActivityFactMetric{
			FactID:      weirdStatsFactRouteHighlights,
			MetricID:    metricID,
			MetricValue: 1,
			Summary:     name,
		})
	}
	return metrics
}

func roadCrossingFactMetrics(statsSnapshot stats.StopStats, roadFact roadCrossingFact) []storage.ActivityFactMetric {
	roadCount := roadFact.Count
	if roadCount <= 0 {
		roadCount = statsSnapshot.RoadCrossingCount
	}
	if summary := buildRoadCrossingPartWithCount(roadCount, roadFact.Roads); summary != "" && roadCount > 0 {
		return []storage.ActivityFactMetric{{
			FactID:      weirdStatsFactRoadCrossings,
			MetricID:    factMetricCount,
			MetricValue: float64(roadCount),
			Summary:     summary,
		}}
	}
	return nil
}

func stopSummaryFactMetrics(statsSnapshot stats.StopStats) []storage.ActivityFactMetric {
	if statsSnapshot.StopCount <= 0 {
		return nil
	}

	summary := stopSummaryFactSummaryFromSnapshot(statsSnapshot.StopCount, statsSnapshot.StopTotalSeconds)
	return []storage.ActivityFactMetric{
		{
			FactID:      weirdStatsFactStopSummary,
			MetricID:    factMetricStopCount,
			MetricValue: float64(statsSnapshot.StopCount),
			Summary:     summary,
		},
		{
			FactID:      weirdStatsFactStopSummary,
			MetricID:    factMetricStopTotal,
			MetricValue: float64(statsSnapshot.StopTotalSeconds),
			Summary:     summary,
		},
	}
}

func trafficLightStopFactMetrics(statsSnapshot stats.StopStats) []storage.ActivityFactMetric {
	if statsSnapshot.TrafficLightStopCount <= 0 {
		return nil
	}
	return []storage.ActivityFactMetric{{
		FactID:      weirdStatsFactTrafficLightStops,
		MetricID:    factMetricCount,
		MetricValue: float64(statsSnapshot.TrafficLightStopCount),
		Summary:     trafficLightStopsFactSummary(statsSnapshot.TrafficLightStopCount),
	}}
}

func stopSummaryFactSummary(stopViews []StopView) string {
	return stopSummaryFactSummaryFromSnapshot(len(stopViews), totalStopSeconds(stopViews))
}

func stopSummaryFactSummaryFromSnapshot(stopCount, stopTotalSeconds int) string {
	summary := formatCountLabel(stopCount, "stop", "stops")
	if stopTotalSeconds > 0 {
		summary += " · " + formatDuration(stopTotalSeconds) + " total"
	}
	return summary
}

func trafficLightStopsFactSummary(count int) string {
	return fmt.Sprintf("%d detected near traffic signals", count)
}

func factMetricNameID(name string) string {
	key := normalizeHighlightName(name)
	if key == "" {
		return ""
	}
	return factMetricPOIPrefix + key
}

func uniqueFactMetricNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := normalizeHighlightName(trimmed)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, trimmed)
	}
	return unique
}
