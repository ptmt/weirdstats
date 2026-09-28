package web

import (
	"fmt"
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
	Count           int
	Confidence      int
	ConfidenceLabel string
	CandidateTime   string
	Candidates      []DraftingCandidateView
}

type DraftingCandidateView struct {
	Interval   string
	Duration   string
	Speed      string
	PowerDrop  string
	Confidence int
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
	highPower  float64
	lowPower   float64
	speed      float64
	speedDiff  float64
	gradeDiff  float64
	confidence int
}

const (
	draftWindowDuration     = 30 * time.Second
	draftMinSpeedMPS        = 7.0
	draftMaxSpeedDiffMPS    = 0.7
	draftMaxGradePercent    = 2.0
	draftMaxGradeDiff       = 0.6
	draftMaxAccelerationMPS = 0.35
	draftMaxSampleGap       = 10 * time.Second
)

func buildDraftingView(activity storage.Activity, points []gps.Point) *DraftingView {
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
	view := &DraftingView{}
	if activity.DeviceWatts == nil {
		view.Status = "Power source unverified"
		view.Detail = "Refresh this ride to check whether Strava watts came from a power meter. Draft clues need measured power."
		view.NeedsRefresh = true
		return view
	}
	if !*activity.DeviceWatts {
		view.Status = "Measured power needed"
		view.Detail = "Strava-estimated watts are calculated partly from speed and elevation, so they cannot independently show a drafting-related power drop."
		return view
	}

	buckets := buildDraftBuckets(points)
	candidates := detectDraftCandidates(buckets)
	if len(candidates) == 0 {
		view.Status = "No clear same-speed drops"
		view.Detail = "No sustained power drops passed the steady-speed, near-flat, and direction checks in this ride. Short rotations may be missed."
		return view
	}

	view.Status = "Possible draft-like shifts"
	view.Count = len(candidates)
	view.Detail = "Adjacent 30-second windows at similar speed, grade, and direction. The score measures pattern strength, not the probability of drafting or time proven at the back."
	if activity.AthleteCount > 1 {
		view.Detail = fmt.Sprintf("Strava grouped %d riders. %s", activity.AthleteCount, view.Detail)
	} else {
		view.Detail = "Strava has not confirmed a group for this ride; scores are capped at 65. " + view.Detail
	}
	var totalDuration time.Duration
	for i := range candidates {
		candidate := &candidates[i]
		candidate.confidence = draftConfidence(*candidate, len(candidates), activity.AthleteCount > 1)
		totalDuration += candidate.end.Sub(candidate.start)
		view.Candidates = append(view.Candidates, DraftingCandidateView{
			Interval:   fmt.Sprintf("%s–%s into ride", formatPacingDuration(candidate.start.Sub(points[0].Time)), formatPacingDuration(candidate.end.Sub(points[0].Time))),
			Duration:   formatDuration(int(candidate.end.Sub(candidate.start).Seconds())),
			Speed:      fmt.Sprintf("%.1f km/h", candidate.speed*3.6),
			PowerDrop:  fmt.Sprintf("%.0f → %.0f W (−%.0f%%)", candidate.highPower, candidate.lowPower, (1-candidate.lowPower/candidate.highPower)*100),
			Confidence: candidate.confidence,
		})
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
	return view
}

func buildDraftBuckets(points []gps.Point) []draftBucket {
	start, end := points[0].Time, points[len(points)-1].Time
	if start.IsZero() || !end.After(start) || end.Sub(start) > 24*time.Hour {
		return nil
	}
	count := int(math.Ceil(end.Sub(start).Seconds() / draftWindowDuration.Seconds()))
	buckets := make([]draftBucket, count)
	for i := range buckets {
		buckets[i].start = start.Add(time.Duration(i) * draftWindowDuration)
	}
	for i := 1; i < len(points); i++ {
		prev, curr := points[i-1], points[i]
		dt := curr.Time.Sub(prev.Time)
		if dt <= 0 || dt > draftMaxSampleGap || !validPacingCoordinate(prev) || !validPacingCoordinate(curr) ||
			!prev.HasPower || !prev.HasGrade || !curr.HasGrade || math.IsNaN(prev.Power) ||
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
			index := int(from.Sub(start) / draftWindowDuration)
			if index < 0 || index >= len(buckets) {
				break
			}
			to := buckets[index].start.Add(draftWindowDuration)
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
	for i := range buckets {
		bucket := &buckets[i]
		if bucket.validSecs < 24 || bucket.distance < 150 {
			continue
		}
		straightness := math.Hypot(bucket.east, bucket.north) / bucket.distance
		if straightness < 0.85 {
			continue
		}
		bucket.speed = bucket.speedSum / bucket.validSecs
		variance := bucket.speedSqSum/bucket.validSecs - bucket.speed*bucket.speed
		if math.Sqrt(math.Max(0, variance)) > 0.6 {
			continue
		}
		bucket.power = bucket.powerSum / bucket.validSecs
		bucket.grade = bucket.gradeSum / bucket.validSecs
		length := math.Hypot(bucket.east, bucket.north)
		bucket.headingEast, bucket.headingNorth = bucket.east/length, bucket.north/length
		bucket.valid = true
	}
	return buckets
}

func detectDraftCandidates(buckets []draftBucket) []draftCandidate {
	var candidates []draftCandidate
	for i := 1; i < len(buckets); i++ {
		high, low := buckets[i-1], buckets[i]
		if !similarDraftWindows(high, low) || high.power < 120 {
			continue
		}
		drop := high.power - low.power
		if drop < math.Max(30, 0.15*high.power) {
			continue
		}
		last, powerSum, speedSum := i, low.power, low.speed
		for j := i + 1; j < len(buckets) && j < i+8; j++ {
			if !similarDraftWindows(high, buckets[j]) || buckets[j].power > high.power*0.85 {
				break
			}
			last, powerSum, speedSum = j, powerSum+buckets[j].power, speedSum+buckets[j].speed
		}
		windowCount := float64(last - i + 1)
		avgLow := powerSum / windowCount
		candidates = append(candidates, draftCandidate{
			start:     low.start,
			end:       buckets[last].start.Add(draftWindowDuration),
			highPower: high.power,
			lowPower:  avgLow,
			speed:     speedSum / windowCount,
			speedDiff: math.Abs(high.speed - speedSum/windowCount),
			gradeDiff: math.Abs(high.grade - low.grade),
		})
		i = last
	}
	return candidates
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
	score := 20 + 8 + 17*clamp01((drop-0.15)/0.25) +
		20*clamp01(1-candidate.speedDiff/draftMaxSpeedDiffMPS) +
		10*clamp01(1-candidate.gradeDiff/draftMaxGradeDiff) +
		10*clamp01(duration/120) +
		5*math.Min(float64(repetitions-1), 3)
	limit := 65.0
	if grouped {
		limit = 90
	}
	if repetitions < 2 {
		limit = math.Min(limit, 65)
	}
	if duration < 60 {
		limit = math.Min(limit, 55)
	}
	return int(math.Min(limit, math.Round(score)))
}

func clamp01(value float64) float64 { return math.Max(0, math.Min(1, value)) }
