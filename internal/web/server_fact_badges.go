package web

import (
	"strings"

	"weirdstats/internal/storage"
)

const factMetricBadgeTolerance = 1e-9

func applyDetectedFactRecordBadges(
	facts []ActivityMapFactView,
	metrics []storage.ActivityFactMetric,
	histories map[string]storage.UserFactMetricHistory,
	year int,
) []ActivityMapFactView {
	if len(facts) == 0 || len(metrics) == 0 || len(histories) == 0 {
		return facts
	}

	metricsByFact := make(map[string][]storage.ActivityFactMetric)
	for _, metric := range metrics {
		metricsByFact[metric.FactID] = append(metricsByFact[metric.FactID], metric)
	}

	out := make([]ActivityMapFactView, len(facts))
	copy(out, facts)
	for i := range out {
		badgeLabel, badgeTone := detectedFactRecordBadge(metricsByFact[out[i].ID], histories, year)
		out[i].BadgeLabel = badgeLabel
		out[i].BadgeTone = badgeTone
	}
	return out
}

func detectedFactRecordBadge(
	metrics []storage.ActivityFactMetric,
	histories map[string]storage.UserFactMetricHistory,
	year int,
) (string, string) {
	if len(metrics) == 0 {
		return "", ""
	}

	hasAllTime := false
	hasYear := false
	for _, metric := range metrics {
		if !factMetricSupportsRecordBadge(metric) {
			continue
		}
		history, ok := histories[metric.FactID+":"+metric.MetricID]
		if !ok {
			continue
		}
		if history.AllTimeSeenCount > 0 && metric.MetricValue >= history.AllTimeBestValue-factMetricBadgeTolerance {
			hasAllTime = true
			break
		}
		if history.YearSeenCount > 0 && metric.MetricValue >= history.YearBestValue-factMetricBadgeTolerance {
			hasYear = true
		}
	}

	if hasAllTime {
		return "All-time best", "record"
	}
	if hasYear && year > 0 {
		return formatYearBadge(year), "year"
	}
	return "", ""
}

func formatYearBadge(year int) string {
	return formatCompactNumber(float64(year), 0) + " best"
}

func factMetricSupportsRecordBadge(metric storage.ActivityFactMetric) bool {
	if strings.HasPrefix(metric.MetricID, factMetricPOIPrefix) {
		return false
	}
	switch metric.MetricID {
	case factMetricDistanceMeters, factMetricStopCount, factMetricStopTotal, factMetricCount, factMetricInverseSeconds:
		return true
	default:
		return false
	}
}
