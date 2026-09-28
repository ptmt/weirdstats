package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"sort"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

// DraftingView describes evidence in one rider's power trace, not rider position.
// Its confidence is a heuristic evidence score, not a calibrated probability.
type DraftingView struct {
	Status          string
	Detail          string
	NeedsRefresh    bool
	CanTune         bool
	WindowSeconds   int
	MinDropPercent  int
	Count           int
	Confidence      int
	ConfidenceLabel string
	CandidateTime   string
	Candidates      []DraftingCandidateView
	Events          []DraftingCandidateView
	RideDurationSec int
	TimelineJSON    template.JS `json:"-"`
	InitialJSON     template.JS `json:"-"`
}

type DraftingCandidateView struct {
	Interval         string
	Duration         string
	Speed            string
	SpeedChange      string
	GradeChange      string
	PowerDrop        string
	Confidence       int
	StartSec         int
	EndSec           int
	BaselineStartSec int
	LowStartSec      int
}

type draftTimelinePoint struct {
	TimeSec int      `json:"t"`
	Power   *float64 `json:"p"`
	Speed   *float64 `json:"s"`
}

type draftBucket struct {
	start        time.Time
	validSecs    float64
	speedSum     float64
	speedSqSum   float64
	powerSum     float64
	gradeSum     float64
	east         float64
	north        float64
	distance     float64
	speed        float64
	power        float64
	grade        float64
	headingEast  float64
	headingNorth float64
	valid        bool
}

type draftCandidate struct {
	start      time.Time
	end        time.Time
	high       draftBucket
	highPower  float64
	lowPower   float64
	speed      float64
	lowGrade   float64
	speedDiff  float64
	gradeDiff  float64
	confidence int
}

const (
	draftBucketDuration     = 5 * time.Second
	draftMinSpeedMPS        = 7.0
	draftMaxSpeedDiffMPS    = 0.5
	draftMaxGradePercent    = 4.0
	draftMaxGradeDiff       = 0.7
	draftMaxAccelerationMPS = 0.35
	draftMaxSampleGap       = 10 * time.Second
)

type draftingOptions struct {
	windowSeconds  int
	minDropPercent int
}

var defaultDraftingOptions = draftingOptions{windowSeconds: 15, minDropPercent: 12}

func buildDraftingView(activity storage.Activity, points []gps.Point) *DraftingView {
	view := buildDraftingViewWithOptions(activity, points, defaultDraftingOptions)
	if view != nil && view.CanTune {
		series := buildDraftTimeline(points)
		if series == nil {
			series = []draftTimelinePoint{}
		}
		if timeline, err := json.Marshal(series); err == nil {
			view.TimelineJSON = template.JS(timeline)
		}
		if initial, err := json.Marshal(view); err == nil {
			view.InitialJSON = template.JS(initial)
		}
	}
	return view
}

func buildDraftingViewWithOptions(activity storage.Activity, points []gps.Point, options draftingOptions) *DraftingView {
	if !isRideType(activity.Type) || len(points) < 2 {
		return nil
	}
	hasPower := false
	for _, point := range points {
		if point.HasPower {
			hasPower = true
			break
		}
	}
	if !hasPower {
		return nil
	}
	view := &DraftingView{WindowSeconds: options.windowSeconds, MinDropPercent: options.minDropPercent, InitialJSON: template.JS("null"), TimelineJSON: template.JS("[]")}
	view.RideDurationSec = int(points[len(points)-1].Time.Sub(points[0].Time).Seconds())
	if activity.DeviceWatts == nil {
		if activity.PowerSourceChecked {
			view.Status = "Power source unavailable from Strava"
			view.Detail = "Strava did not report whether these watts came from a power meter, so draft clues cannot be scored reliably for this ride."
			return view
		}
		view.Status = "Power source unverified"
		view.Detail = "Check whether Strava recorded these watts from a power meter. Draft clues need measured power."
		view.NeedsRefresh = true
		return view
	}
	if !*activity.DeviceWatts {
		view.Status = "Measured power needed"
		view.Detail = "Strava-estimated watts are calculated partly from speed and elevation, so they cannot independently show a drafting-related power drop."
		return view
	}
	view.CanTune = true

	buckets := buildDraftBuckets(points)
	candidates := detectDraftCandidates(buckets, options)
	if len(candidates) == 0 {
		view.Status = "No clear same-speed drops"
		view.Detail = fmt.Sprintf("No power drop of at least %d%% and 20 W passed the %d-second speed, grade, and direction checks. Try a shorter window for quick rotations.", options.minDropPercent, options.windowSeconds)
		return view
	}

	view.Status = "Possible draft-like shifts"
	view.Count = len(candidates)
	view.Detail = "Power fell while speed stayed steady. Select a marker to inspect the compared windows. Scores describe pattern strength, not rider position."
	if activity.AthleteCount > 1 {
		view.Detail = fmt.Sprintf("Strava grouped %d riders. %s", activity.AthleteCount, view.Detail)
	} else {
		view.Detail = "Strava group count unavailable; maximum score 65. " + view.Detail
	}
	var totalDuration time.Duration
	for i := range candidates {
		candidate := &candidates[i]
		candidate.confidence = draftConfidence(*candidate, len(candidates), activity.AthleteCount > 1)
		totalDuration += candidate.end.Sub(candidate.start)
		event := DraftingCandidateView{
			Interval:         fmt.Sprintf("%s → %s after start", formatDraftElapsed(candidate.start.Sub(points[0].Time)), formatDraftElapsed(candidate.end.Sub(points[0].Time))),
			Duration:         formatDuration(int(candidate.end.Sub(candidate.start).Seconds())),
			Speed:            fmt.Sprintf("%.1f km/h", candidate.speed*3.6),
			SpeedChange:      fmt.Sprintf("%.1f → %.1f km/h", candidate.high.speed*3.6, candidate.speed*3.6),
			GradeChange:      fmt.Sprintf("%+.1f%% → %+.1f%%", candidate.high.grade, candidate.lowGrade),
			PowerDrop:        fmt.Sprintf("%.0f → %.0f W (−%.0f%%)", candidate.highPower, candidate.lowPower, (1-candidate.lowPower/candidate.highPower)*100),
			Confidence:       candidate.confidence,
			StartSec:         int(candidate.start.Sub(points[0].Time).Seconds()),
			EndSec:           int(candidate.end.Sub(points[0].Time).Seconds()),
			BaselineStartSec: int(candidate.high.start.Sub(points[0].Time).Seconds()),
			LowStartSec:      int(candidate.high.start.Sub(points[0].Time).Seconds()) + options.windowSeconds,
		}
		view.Events = append(view.Events, event)
		view.Candidates = append(view.Candidates, event)
		if candidate.confidence > view.Confidence {
			view.Confidence = candidate.confidence
		}
	}
	view.CandidateTime = formatDuration(int(totalDuration.Seconds()))
	view.ConfidenceLabel = "Exploratory"
	if view.Confidence >= 75 {
		view.ConfidenceLabel = "Strong clue"
	} else if view.Confidence >= 50 {
		view.ConfidenceLabel = "Moderate clue"
	}
	sort.Slice(view.Candidates, func(i, j int) bool { return view.Candidates[i].Confidence > view.Candidates[j].Confidence })
	if len(view.Candidates) > 5 {
		view.Candidates = view.Candidates[:5]
	}
	sort.Slice(view.Candidates, func(i, j int) bool { return view.Candidates[i].StartSec < view.Candidates[j].StartSec })
	return view
}

func formatDraftElapsed(duration time.Duration) string {
	seconds := max(0, int(duration.Seconds()))
	if seconds >= 3600 {
		return fmt.Sprintf("%dh %02dm %02ds", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
}

func buildDraftTimeline(points []gps.Point) []draftTimelinePoint {
	if len(points) < 2 {
		return nil
	}
	duration := int(points[len(points)-1].Time.Sub(points[0].Time).Seconds())
	if duration <= 0 || duration > 24*3600 {
		return nil
	}
	type sampleBucket struct {
		powerSum, speedSum     float64
		powerCount, speedCount int
	}
	buckets := make([]sampleBucket, duration/5+1)
	for _, point := range points {
		second := int(point.Time.Sub(points[0].Time).Seconds())
		if second < 0 || second > duration {
			continue
		}
		bucket := &buckets[second/5]
		if point.HasPower && !math.IsNaN(point.Power) && !math.IsInf(point.Power, 0) && point.Power >= 0 {
			bucket.powerSum += point.Power
			bucket.powerCount++
		}
		if !math.IsNaN(point.Speed) && !math.IsInf(point.Speed, 0) && point.Speed >= 0 {
			bucket.speedSum += point.Speed * 3.6
			bucket.speedCount++
		}
	}
	timeline := make([]draftTimelinePoint, len(buckets))
	for index, bucket := range buckets {
		timeline[index].TimeSec = index * 5
		if bucket.powerCount > 0 {
			value := bucket.powerSum / float64(bucket.powerCount)
			timeline[index].Power = &value
		}
		if bucket.speedCount > 0 {
			value := bucket.speedSum / float64(bucket.speedCount)
			timeline[index].Speed = &value
		}
	}
	return timeline
}

func buildDraftBuckets(points []gps.Point) []draftBucket {
	start, end := points[0].Time, points[len(points)-1].Time
	if start.IsZero() || !end.After(start) || end.Sub(start) > 24*time.Hour {
		return nil
	}
	count := int(math.Ceil(end.Sub(start).Seconds() / draftBucketDuration.Seconds()))
	buckets := make([]draftBucket, count)
	for i := range buckets {
		buckets[i].start = start.Add(time.Duration(i) * draftBucketDuration)
	}
	for i := 1; i < len(points); i++ {
		prev, curr := points[i-1], points[i]
		dt := curr.Time.Sub(prev.Time)
		if dt <= 0 || dt > draftMaxSampleGap || !validPacingCoordinate(prev) || !validPacingCoordinate(curr) ||
			!prev.HasPower || !prev.HasGrade || !curr.HasGrade ||
			math.IsNaN(prev.Power) || math.IsInf(prev.Power, 0) ||
			math.IsNaN(prev.Speed) || math.IsInf(prev.Speed, 0) || math.IsNaN(curr.Speed) || math.IsInf(curr.Speed, 0) ||
			math.IsNaN(prev.Grade) || math.IsInf(prev.Grade, 0) || math.IsNaN(curr.Grade) || math.IsInf(curr.Grade, 0) ||
			prev.Power < 0 || prev.Speed < draftMinSpeedMPS || curr.Speed < draftMinSpeedMPS ||
			math.Abs(prev.Grade) > draftMaxGradePercent || math.Abs(curr.Grade) > draftMaxGradePercent ||
			math.Abs(curr.Speed-prev.Speed)/dt.Seconds() > draftMaxAccelerationMPS {
			continue
		}
		edge := haversineMeters(prev.Lat, prev.Lon, curr.Lat, curr.Lon)
		if edge <= 0 || edge > maxPacingSpeedMPS*dt.Seconds()+20 {
			continue
		}
		meanLat := (prev.Lat + curr.Lat) / 2 * math.Pi / 180
		east := (curr.Lon - prev.Lon) * 111195 * math.Cos(meanLat)
		north := (curr.Lat - prev.Lat) * 111195
		for from := prev.Time; from.Before(curr.Time); {
			index := int(from.Sub(start) / draftBucketDuration)
			if index < 0 || index >= len(buckets) {
				break
			}
			to := buckets[index].start.Add(draftBucketDuration)
			if to.After(curr.Time) {
				to = curr.Time
			}
			seconds := to.Sub(from).Seconds()
			fraction := seconds / dt.Seconds()
			bucket := &buckets[index]
			bucket.validSecs += seconds
			bucket.speedSum += prev.Speed * seconds
			bucket.speedSqSum += prev.Speed * prev.Speed * seconds
			bucket.powerSum += prev.Power * seconds
			bucket.gradeSum += prev.Grade * seconds
			bucket.east += east * fraction
			bucket.north += north * fraction
			bucket.distance += edge * fraction
			from = to
		}
	}
	return buckets
}

func aggregateDraftWindow(buckets []draftBucket, first, count int) draftBucket {
	if first < 0 || first+count > len(buckets) {
		return draftBucket{}
	}
	window := draftBucket{start: buckets[first].start}
	for _, bucket := range buckets[first : first+count] {
		window.validSecs += bucket.validSecs
		window.speedSum += bucket.speedSum
		window.speedSqSum += bucket.speedSqSum
		window.powerSum += bucket.powerSum
		window.gradeSum += bucket.gradeSum
		window.east += bucket.east
		window.north += bucket.north
		window.distance += bucket.distance
	}
	if window.validSecs < 0.8*float64(count)*draftBucketDuration.Seconds() || window.distance <= 0 {
		return window
	}
	window.speed = window.speedSum / window.validSecs
	variance := window.speedSqSum/window.validSecs - window.speed*window.speed
	if window.speed < draftMinSpeedMPS || math.Sqrt(math.Max(0, variance)) > 0.45 ||
		math.Hypot(window.east, window.north)/window.distance < 0.85 {
		return window
	}
	window.power = window.powerSum / window.validSecs
	window.grade = window.gradeSum / window.validSecs
	length := math.Hypot(window.east, window.north)
	window.headingEast, window.headingNorth = window.east/length, window.north/length
	window.valid = true
	return window
}

func detectDraftCandidates(buckets []draftBucket, options draftingOptions) []draftCandidate {
	width := options.windowSeconds / int(draftBucketDuration.Seconds())
	if width < 1 {
		return nil
	}
	var candidates []draftCandidate
	for first := width; first+width <= len(buckets); first++ {
		high := aggregateDraftWindow(buckets, first-width, width)
		low := aggregateDraftWindow(buckets, first, width)
		if !similarDraftWindows(high, low) || high.power < 120 {
			continue
		}
		if high.power-low.power < math.Max(20, float64(options.minDropPercent)*high.power/100) {
			continue
		}
		candidate := draftCandidate{
			start: low.start, end: low.start.Add(time.Duration(options.windowSeconds) * time.Second),
			high: high, highPower: high.power, lowPower: low.power, speed: low.speed, lowGrade: low.grade,
			speedDiff: math.Abs(high.speed - low.speed), gradeDiff: math.Abs(high.grade - low.grade),
		}
		if len(candidates) > 0 && !candidate.start.After(candidates[len(candidates)-1].end) {
			last := &candidates[len(candidates)-1]
			if candidate.end.After(last.end) {
				last.end = candidate.end
			}
			if candidate.highPower-candidate.lowPower > last.highPower-last.lowPower {
				start, end := last.start, last.end
				*last = candidate
				last.start, last.end = start, end
			}
			continue
		}
		candidates = append(candidates, candidate)
	}
	// The transition windows identify the shift. Extend an episode only while
	// successive complete windows remain lower at comparable speed and grade.
	for i := range candidates {
		candidate := &candidates[i]
		for candidate.end.Sub(candidate.start) < 4*time.Minute {
			first := int(candidate.end.Sub(buckets[0].start) / draftBucketDuration)
			next := aggregateDraftWindow(buckets, first, width)
			if !similarDraftWindows(candidate.high, next) ||
				next.power > candidate.highPower*(1-0.7*float64(options.minDropPercent)/100) {
				break
			}
			candidate.end = next.start.Add(time.Duration(options.windowSeconds) * time.Second)
		}
	}
	merged := make([]draftCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if len(merged) == 0 || candidate.start.After(merged[len(merged)-1].end) {
			merged = append(merged, candidate)
			continue
		}
		last := &merged[len(merged)-1]
		start, end := last.start, last.end
		if candidate.end.After(end) {
			end = candidate.end
		}
		if candidate.highPower-candidate.lowPower > last.highPower-last.lowPower {
			*last = candidate
		}
		last.start, last.end = start, end
	}
	return merged
}

func similarDraftWindows(a, b draftBucket) bool {
	if !a.valid || !b.valid || math.Abs(a.speed-b.speed) > draftMaxSpeedDiffMPS ||
		math.Abs(a.grade-b.grade) > draftMaxGradeDiff {
		return false
	}
	return a.headingEast*b.headingEast+a.headingNorth*b.headingNorth >= math.Cos(25*math.Pi/180)
}

func draftConfidence(candidate draftCandidate, repetitions int, grouped bool) int {
	drop := 1 - candidate.lowPower/candidate.highPower
	duration := candidate.end.Sub(candidate.start).Seconds()
	score := 5 + 17*clamp01((drop-0.08)/0.25) +
		20*clamp01(1-candidate.speedDiff/draftMaxSpeedDiffMPS) +
		10*clamp01(1-candidate.gradeDiff/draftMaxGradeDiff) +
		15*clamp01(duration/90) +
		8*math.Min(float64(repetitions-1), 3)
	if grouped {
		score += 6
	}
	limit := 65.0
	if grouped {
		limit = 90
	}
	if duration < 20 {
		limit = math.Min(limit, 40)
	} else if duration < 45 {
		limit = math.Min(limit, 55)
	}
	if repetitions < 2 && !grouped {
		limit = math.Min(limit, 60)
	}
	return int(math.Min(limit, math.Round(score)))
}

func clamp01(value float64) float64 { return math.Max(0, math.Min(1, value)) }
