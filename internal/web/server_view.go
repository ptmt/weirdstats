package web

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
	"unicode"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

func formatDuration(totalSeconds int) string {
	if totalSeconds <= 0 {
		return "0m"
	}
	duration := time.Duration(totalSeconds) * time.Second
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format("Jan 2, 2006 15:04")
}

func formatDistance(meters float64) string {
	if meters <= 0 {
		return ""
	}
	km := meters / 1000
	if km >= 10 {
		return fmt.Sprintf("%.1f km", km)
	}
	return fmt.Sprintf("%.2f km", km)
}

func buildActivityDataItems(
	description string,
	points []gps.Point,
	stops []storage.ActivityStop,
	statsSnapshot stats.StopStats,
	statsPresent bool,
	detectedFacts []ActivityMapFactView,
	detectedFactsPresent bool,
	stopOpts gps.StopOptions,
	mapAPIAvailable bool,
	overpassAvailable bool,
) []ActivityDataItem {
	items := []ActivityDataItem{
		buildDescriptionDataItem(description),
		buildRoutePointsDataItem(points),
		buildStopDetectionDataItem(points, stops, statsPresent, stopOpts),
		buildStatsSnapshotDataItem(statsSnapshot, statsPresent),
		buildDetectedFactsDataItem(detectedFacts, detectedFactsPresent, points, stops),
		buildEnrichmentDataItem(mapAPIAvailable, overpassAvailable),
	}
	return items
}

func buildDescriptionDataItem(description string) ActivityDataItem {
	plainDescription, detectedFactCount := splitStoredActivityDescription(description)

	item := ActivityDataItem{
		Label:  "Description",
		Value:  "empty",
		Detail: "No Strava description is stored for this activity yet.",
		Tone:   "warning",
	}
	if plainDescription != "" {
		item.Value = "present"
		item.Detail = "Strava description text is stored."
		item.Tone = "ok"
	}
	if detectedFactCount > 0 {
		item.Detail = fmt.Sprintf("%s Managed weirdstats line currently contains %s.", item.Detail, formatCountLabel(detectedFactCount, "posted fact", "posted facts"))
		if plainDescription == "" {
			item.Value = "managed only"
		}
		item.Tone = "ok"
	}
	return item
}

func buildRoutePointsDataItem(points []gps.Point) ActivityDataItem {
	count := len(points)
	item := ActivityDataItem{
		Label: "Route points",
		Value: formatCountLabel(count, "point", "points"),
		Tone:  "ok",
	}

	switch {
	case count == 0:
		item.Detail = "No GPS route points are stored, so the map, speed chart, and stop detection cannot run."
		item.Tone = "warning"
	case count == 1:
		item.Detail = "Only one GPS point is stored. The activity can be located, but route and stop windows cannot be reconstructed."
		item.Tone = "warning"
	default:
		item.Detail = "Stored GPS points power the route map, speed chart, stop detection, and route-linked facts."
	}
	return item
}

func buildStopDetectionDataItem(points []gps.Point, stops []storage.ActivityStop, statsPresent bool, stopOpts gps.StopOptions) ActivityDataItem {
	item := ActivityDataItem{
		Label: "Stop detection",
	}

	if !statsPresent {
		item.Value = "pending"
		item.Detail = fmt.Sprintf("Stop processing has not written stop stats yet. A stored stop needs speed at or below %.1f m/s for at least %s.", stopOpts.SpeedThreshold, formatDuration(int(stopOpts.MinDuration.Seconds())))
		item.Tone = "pending"
		return item
	}

	item.Value = formatCountLabel(len(stops), "stop", "stops")
	summary := summarizeLowSpeedWindows(points, stopOpts.SpeedThreshold)
	minDuration := formatDuration(int(stopOpts.MinDuration.Seconds()))

	switch {
	case len(points) == 0:
		item.Detail = "Stop processing completed, but no route points are stored, so no stop windows could be derived."
		item.Tone = "warning"
	case len(points) == 1:
		item.Detail = "Stop processing completed, but only one route point is stored, so stop windows cannot be derived."
		item.Tone = "warning"
	case len(stops) > 0:
		item.Detail = fmt.Sprintf("Stop processing completed and wrote %s from stored route points using speed at or below %.1f m/s for at least %s.", formatCountLabel(len(stops), "stored stop", "stored stops"), stopOpts.SpeedThreshold, minDuration)
		item.Tone = "ok"
	case summary.WindowCount == 0:
		item.Detail = fmt.Sprintf("Stop processing completed. No stored speed samples dropped to %.1f m/s or below, so there were no candidate stop windows.", stopOpts.SpeedThreshold)
		item.Tone = "warning"
	case summary.LongestDuration < stopOpts.MinDuration:
		item.Detail = fmt.Sprintf("Stop processing completed. %s found; the longest lasted %s. A stored stop needs at least %s at or below %.1f m/s.", formatCountLabel(summary.WindowCount, "candidate low-speed window", "candidate low-speed windows"), formatDuration(int(summary.LongestDuration.Seconds())), minDuration, stopOpts.SpeedThreshold)
		item.Tone = "warning"
	default:
		item.Detail = fmt.Sprintf("Stop processing completed. %s found, but none qualified as stored stops.", formatCountLabel(summary.WindowCount, "candidate low-speed window", "candidate low-speed windows"))
		item.Tone = "warning"
	}

	return item
}

func buildStatsSnapshotDataItem(statsSnapshot stats.StopStats, statsPresent bool) ActivityDataItem {
	item := ActivityDataItem{
		Label: "Stats snapshot",
	}
	if !statsPresent {
		item.Value = "pending"
		item.Detail = "No processed activity-stats row is stored yet."
		item.Tone = "pending"
		return item
	}

	item.Value = "present"
	item.Detail = fmt.Sprintf("%d stops · %s total · %d at lights · %d crossings", statsSnapshot.StopCount, formatDuration(statsSnapshot.StopTotalSeconds), statsSnapshot.TrafficLightStopCount, statsSnapshot.RoadCrossingCount)
	if !statsSnapshot.UpdatedAt.IsZero() {
		item.Detail += " · updated " + formatTimestamp(statsSnapshot.UpdatedAt)
	}
	item.Tone = "ok"
	return item
}

func buildDetectedFactsDataItem(detectedFacts []ActivityMapFactView, detectedFactsPresent bool, points []gps.Point, stops []storage.ActivityStop) ActivityDataItem {
	item := ActivityDataItem{
		Label: "Map-linked facts",
	}
	switch {
	case !detectedFactsPresent:
		item.Value = "pending"
		item.Detail = "Detected facts have not been precomputed and stored yet."
		item.Tone = "pending"
	case len(detectedFacts) > 0:
		item.Value = formatCountLabel(len(detectedFacts), "fact", "facts")
		item.Detail = "Built from the current route points and stored stop events."
		item.Tone = "ok"
	case len(points) == 0:
		item.Value = "0 facts"
		item.Detail = "No route-linked facts can be derived until route points are stored."
		item.Tone = "warning"
	case len(stops) == 0:
		item.Value = "0 facts"
		item.Detail = "Only route-derived facts are possible right now because no stored stop events exist."
		item.Tone = "warning"
	default:
		item.Value = "0 facts"
		item.Detail = "No map-linked facts were derived from the current route and stop data."
		item.Tone = "warning"
	}
	return item
}

func buildEnrichmentDataItem(mapAPIAvailable bool, overpassAvailable bool) ActivityDataItem {
	item := ActivityDataItem{
		Label: "Enrichment",
	}
	coffeePause := formatDuration(int(coffeeStopMinDuration.Seconds()))

	switch {
	case mapAPIAvailable && overpassAvailable:
		item.Value = "overpass available"
		item.Detail = fmt.Sprintf("Traffic-light matching was available during stop processing, and Overpass-backed detections can run for coffee stops, route highlights, and named road crossings. Coffee stops specifically need a ride and a qualifying pause of at least %s.", coffeePause)
		item.Tone = "ok"
	case mapAPIAvailable:
		item.Value = "overpass unavailable"
		item.Detail = fmt.Sprintf("Traffic-light matching was available during stop processing, but Overpass-backed detections are unavailable, so coffee stops, route highlights, and named road crossings cannot be enriched. Coffee stops specifically need Overpass plus a qualifying pause of at least %s.", coffeePause)
		item.Tone = "warning"
	case overpassAvailable:
		item.Value = "lights unavailable"
		item.Detail = fmt.Sprintf("Overpass-backed detections can run for coffee stops, route highlights, and named road crossings, but traffic-light matching was unavailable during stop processing. Coffee stops still need a ride and a qualifying pause of at least %s.", coffeePause)
		item.Tone = "warning"
	default:
		item.Value = "route-only"
		item.Detail = fmt.Sprintf("Neither traffic-light map matching nor Overpass-backed enrichment is available. Coffee stops, route highlights, and named road crossings cannot be enriched, and coffee stops would still need a qualifying pause of at least %s.", coffeePause)
		item.Tone = "warning"
	}

	return item
}

type lowSpeedWindowSummary struct {
	WindowCount     int
	LongestDuration time.Duration
}

func summarizeLowSpeedWindows(points []gps.Point, threshold float64) lowSpeedWindowSummary {
	if len(points) == 0 {
		return lowSpeedWindowSummary{}
	}

	var summary lowSpeedWindowSummary
	var inWindow bool
	var startPoint gps.Point
	var lastPoint gps.Point

	for i, point := range points {
		if i == 0 {
			lastPoint = point
		}

		if point.Speed <= threshold {
			if !inWindow {
				inWindow = true
				startPoint = point
			}
		} else if inWindow {
			duration := lastPoint.Time.Sub(startPoint.Time)
			summary.WindowCount++
			if duration > summary.LongestDuration {
				summary.LongestDuration = duration
			}
			inWindow = false
		}

		lastPoint = point
	}

	if inWindow {
		duration := lastPoint.Time.Sub(startPoint.Time)
		summary.WindowCount++
		if duration > summary.LongestDuration {
			summary.LongestDuration = duration
		}
	}

	return summary
}

func formatCountLabel(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func (s *Server) buildContributionData(ctx context.Context, userID int64, now time.Time) ContributionData {
	return s.buildContributionDataForYear(ctx, userID, now.Year(), now)
}

func (s *Server) buildContributionDataForYear(ctx context.Context, userID int64, year int, now time.Time) ContributionData {
	loc := time.Local
	start := time.Date(year, time.January, 1, 0, 0, 0, 0, loc)
	end := time.Date(year, time.December, 31, 0, 0, 0, 0, loc)
	rangeEnd := end
	if year == now.Year() {
		rangeEnd = time.Date(year, now.Month(), now.Day(), 0, 0, 0, 0, loc)
	}
	startGrid := start
	for startGrid.Weekday() != time.Monday {
		startGrid = startGrid.AddDate(0, 0, -1)
	}
	endGrid := end
	for endGrid.Weekday() != time.Sunday {
		endGrid = endGrid.AddDate(0, 0, 1)
	}

	activities, err := s.store.ListActivityTimes(ctx, userID, startGrid, rangeEnd.AddDate(0, 0, 1))
	if err != nil {
		log.Printf("contrib load failed: %v", err)
	}

	effortByDay := make(map[string]float64)
	for _, activity := range activities {
		if activity.MovingTime <= 0 {
			continue
		}
		dayKey := activity.StartTime.In(loc).Format("2006-01-02")
		effort := 0.0
		if activity.EffortVersion > 0 && activity.EffortScore > 0 {
			effort = activity.EffortScore / 60.0
		} else {
			effort = float64(activity.MovingTime) / 3600
		}
		if effort <= 0 {
			continue
		}
		effortByDay[dayKey] += effort
	}

	maxEffort := 0.0
	totalEffort := 0.0
	for day := start; !day.After(rangeEnd); day = day.AddDate(0, 0, 1) {
		effort := effortByDay[day.Format("2006-01-02")]
		if effort > maxEffort {
			maxEffort = effort
		}
		totalEffort += effort
	}

	var days []ContributionDay
	var months []ContributionMonth
	weekIndex := 0
	for weekStart := startGrid; !weekStart.After(endGrid); weekStart = weekStart.AddDate(0, 0, 7) {
		weekIndex++
		for i := 0; i < 7; i++ {
			day := weekStart.AddDate(0, 0, i)
			if day.Before(start) || day.After(end) {
				continue
			}
			if day.Day() == 1 {
				months = append(months, ContributionMonth{
					Label:  day.Format("Jan"),
					Column: weekIndex,
				})
				break
			}
		}
		for i := 0; i < 7; i++ {
			day := weekStart.AddDate(0, 0, i)
			inYear := !day.Before(start) && !day.After(end)
			inRange := !day.Before(start) && !day.After(rangeEnd)
			dateKey := day.Format("2006-01-02")
			effort := 0.0
			if inRange {
				effort = effortByDay[dateKey]
			}
			level := 0
			if inRange {
				level = contributionLevel(effort)
			}
			effortLabel := ""
			if inRange {
				effortLabel = formatEffort(effort)
			}
			days = append(days, ContributionDay{
				Date:        dateKey,
				Label:       day.Format("Jan 2, 2006"),
				Tooltip:     contributionTooltip(day, inRange, inYear, effortLabel, year),
				Effort:      effort,
				EffortLabel: effortLabel,
				Level:       level,
				InRange:     inRange,
			})
		}
	}

	weeks := weekIndex
	if weeks < 1 {
		weeks = 1
	}

	return ContributionData{
		Days:        days,
		Months:      months,
		Weeks:       weeks,
		Year:        year,
		Levels:      contributionMaxLevel,
		StartLabel:  start.Format("Jan 2, 2006"),
		EndLabel:    end.Format("Jan 2, 2006"),
		MaxEffort:   maxEffort,
		TotalEffort: totalEffort,
	}
}

func contributionTooltip(day time.Time, inRange, inYear bool, effortLabel string, year int) string {
	label := day.Format("Mon, Jan 2, 2006")
	switch {
	case inRange:
		if effortLabel == "" {
			return label
		}
		return fmt.Sprintf("%s · %s", label, effortLabel)
	case inYear:
		return fmt.Sprintf("%s · Future day", label)
	default:
		return fmt.Sprintf("%s · Outside %d", label, year)
	}
}

func buildRoutePreviewPath(points []storage.ActivityRoutePoint, width, height, padding float64) (string, float64, float64, float64, float64, bool) {
	if len(points) < 2 {
		return "", 0, 0, 0, 0, false
	}
	if width <= (padding*2) || height <= (padding*2) {
		return "", 0, 0, 0, 0, false
	}

	minLat, maxLat := points[0].Lat, points[0].Lat
	minLon, maxLon := points[0].Lon, points[0].Lon
	for _, point := range points[1:] {
		if point.Lat < minLat {
			minLat = point.Lat
		}
		if point.Lat > maxLat {
			maxLat = point.Lat
		}
		if point.Lon < minLon {
			minLon = point.Lon
		}
		if point.Lon > maxLon {
			maxLon = point.Lon
		}
	}

	latSpan := maxLat - minLat
	lonSpan := maxLon - minLon
	if latSpan == 0 && lonSpan == 0 {
		return "", 0, 0, 0, 0, false
	}

	innerWidth := width - (padding * 2)
	innerHeight := height - (padding * 2)
	if latSpan == 0 {
		latSpan = 1
	}
	if lonSpan == 0 {
		lonSpan = 1
	}

	scale := math.Min(innerWidth/lonSpan, innerHeight/latSpan)
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return "", 0, 0, 0, 0, false
	}

	routeWidth := (maxLon - minLon) * scale
	routeHeight := (maxLat - minLat) * scale
	offsetX := padding + ((innerWidth - routeWidth) / 2)
	offsetY := padding + ((innerHeight - routeHeight) / 2)

	var path strings.Builder
	path.Grow(len(points) * 14)

	pointCount := 0
	var startX, startY, endX, endY float64
	for _, point := range points {
		x := offsetX
		if maxLon != minLon {
			x += (point.Lon - minLon) * scale
		}
		y := offsetY
		if maxLat != minLat {
			y += (maxLat - point.Lat) * scale
		}

		if pointCount == 0 {
			fmt.Fprintf(&path, "M %.2f %.2f", x, y)
			startX, startY = x, y
		} else {
			fmt.Fprintf(&path, " L %.2f %.2f", x, y)
		}
		endX, endY = x, y
		pointCount++
	}

	if pointCount < 2 || (startX == endX && startY == endY) {
		return "", 0, 0, 0, 0, false
	}
	return path.String(), startX, startY, endX, endY, true
}

func enrichActivityView(view *ActivityView, activity storage.Activity) {
	view.TypeLabel = activityTypeLabel(activity.Type)
	view.TypeClass = activityTypeClass(activity.Type)
	view.IsHidden = isActivityHidden(activity)
	view.FeedMuted = activity.HideFromHome
	view.DistanceValue, view.DistanceUnit = formatDistanceParts(activity.Distance)
	view.PaceLabel, view.PaceValue, view.PaceUnit = formatPaceOrSpeed(activity.Type, activity.Distance, activity.MovingTime)
	view.PowerValue, view.PowerUnit, view.HasPower = formatPower(activity.AveragePower)
}

func formatDistanceParts(meters float64) (string, string) {
	if meters <= 0 {
		return "—", ""
	}
	km := meters / 1000
	if km >= 10 {
		return fmt.Sprintf("%.1f", km), "km"
	}
	return fmt.Sprintf("%.2f", km), "km"
}

func formatPaceOrSpeed(activityType string, meters float64, seconds int) (string, string, string) {
	if isPaceType(activityType) {
		value, unit := formatPace(meters, seconds)
		return "Pace", value, unit
	}
	value, unit := formatSpeed(meters, seconds)
	return "Avg speed", value, unit
}

func formatPace(meters float64, seconds int) (string, string) {
	if meters <= 0 || seconds <= 0 {
		return "—", ""
	}
	paceSeconds := int(math.Round(float64(seconds) / (meters / 1000)))
	minutes := paceSeconds / 60
	remaining := paceSeconds % 60
	return fmt.Sprintf("%d:%02d", minutes, remaining), "/km"
}

func formatSpeed(meters float64, seconds int) (string, string) {
	if meters <= 0 || seconds <= 0 {
		return "—", ""
	}
	hours := float64(seconds) / 3600
	speed := (meters / 1000) / hours
	return fmt.Sprintf("%.1f", speed), "km/h"
}

func formatPower(watts float64) (string, string, bool) {
	if watts <= 0 {
		return "—", "", false
	}
	return fmt.Sprintf("%.0f", math.Round(watts)), "W", true
}

func formatEffort(effort float64) string {
	if effort <= 0 {
		return "No effort"
	}
	if effort < 10 {
		return fmt.Sprintf("Effort %.1f h", effort)
	}
	return fmt.Sprintf("Effort %.0f h", effort)
}

const contributionMaxLevel = 11

func contributionLevel(effort float64) int {
	if effort <= 0 {
		return 0
	}
	switch {
	case effort < 1:
		return 1
	case effort < 2:
		return 2
	case effort < 3:
		return 3
	case effort < 4:
		return 4
	case effort < 5:
		return 5
	case effort < 6:
		return 6
	case effort < 7:
		return 7
	case effort < 8:
		return 8
	case effort < 9:
		return 9
	case effort < 10:
		return 10
	default:
		return 11
	}
}

func activityTypeClass(activityType string) string {
	t := strings.ToLower(activityType)
	switch {
	case strings.Contains(t, "ride"):
		return "ride"
	case strings.Contains(t, "run"):
		return "run"
	case strings.Contains(t, "swim"):
		return "swim"
	case t == "walk" || t == "hike":
		return "walk"
	case strings.Contains(t, "workout") || strings.Contains(t, "training") || t == "yoga":
		return "workout"
	default:
		return "other"
	}
}

func activityTypeLabel(activityType string) string {
	if activityType == "" {
		return "Activity"
	}
	return splitCamelCase(activityType)
}

func splitCamelCase(input string) string {
	runes := []rune(input)
	if len(runes) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runes {
		if r == '_' || r == '-' {
			b.WriteRune(' ')
			continue
		}
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isPaceType(activityType string) bool {
	t := strings.ToLower(activityType)
	if strings.Contains(t, "run") {
		return true
	}
	switch t {
	case "walk", "hike":
		return true
	default:
		return false
	}
}

func isActivityHidden(activity storage.Activity) bool {
	if activity.HiddenByRule {
		return true
	}
	if activity.HideFromHome || activity.IsPrivate {
		return true
	}
	if strings.EqualFold(activity.Visibility, "only_me") || strings.EqualFold(activity.Visibility, "private") {
		return true
	}
	return false
}
