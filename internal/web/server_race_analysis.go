package web

import (
	"fmt"
	"math"
	"sort"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

// RidePacingView is calculated from the stored GPS trace on each detail request.
// The times are elapsed times, so a pause is part of the split in which it occurs.
type RidePacingView struct {
	Distance        string
	FastestFiveKM   string
	FastestFiveFrom string
	FinishChange    string
	FinishDetail    string
	FastestSplit    string
	Splits          []RidePacingSplitView
}

type RidePacingSplitView struct {
	Label        string
	Distance     string
	Duration     string
	Speed        string
	Power        string
	HeartRate    string
	HasPower     bool
	HasHeartRate bool
	Fastest      bool
}

type pacingPoint struct {
	distance     float64
	time         time.Time
	power        float64
	heartRate    float64
	hasPower     bool
	hasHeartRate bool
}

const (
	minPacingDistanceMeters = 4000
	fastFiveDistanceMeters  = 5000
	maxPacingSpeedMPS       = 35
)

func buildRidePacingView(activity storage.Activity, points []gps.Point) *RidePacingView {
	if !isRideType(activity.Type) || len(points) < 2 {
		return nil
	}
	track := buildPacingTrack(points)
	if len(track) < 2 {
		return nil
	}
	total := track[len(track)-1].distance
	if total < minPacingDistanceMeters {
		return nil
	}
	// Large disagreement means the GPS trace is incomplete or contains jumps.
	// Its split boundaries would look precise while representing the wrong distance.
	if activity.Distance > 0 && (total/activity.Distance < 0.8 || total/activity.Distance > 1.2) {
		return nil
	}

	view := &RidePacingView{Distance: formatDistance(total)}
	view.Splits = make([]RidePacingSplitView, 0, 4)
	fastestSpeed := 0.0
	var firstSpeed, lastSpeed float64
	for i := 0; i < 4; i++ {
		from, to := total*float64(i)/4, total*float64(i+1)/4
		start, okStart := pacingTimeAt(track, from)
		end, okEnd := pacingTimeAt(track, to)
		if !okStart || !okEnd || !end.After(start) {
			return nil
		}
		seconds := end.Sub(start).Seconds()
		speed := (to - from) / seconds * 3.6
		power, hasPower, heartRate, hasHeartRate := pacingSensorAverages(track, start, end)
		split := RidePacingSplitView{
			Label:        fmt.Sprintf("Quarter %d", i+1),
			Distance:     fmt.Sprintf("%.1f–%.1f km", from/1000, to/1000),
			Duration:     formatPacingDuration(end.Sub(start)),
			Speed:        fmt.Sprintf("%.1f km/h", speed),
			HasPower:     hasPower,
			HasHeartRate: hasHeartRate,
		}
		if hasPower {
			split.Power = fmt.Sprintf("%.0f W", power)
		}
		if hasHeartRate {
			split.HeartRate = fmt.Sprintf("%.0f bpm", heartRate)
		}
		if speed > fastestSpeed {
			fastestSpeed = speed
			view.FastestSplit = split.Label
		}
		if i == 0 {
			firstSpeed = speed
		}
		if i == 3 {
			lastSpeed = speed
		}
		view.Splits = append(view.Splits, split)
	}
	for i := range view.Splits {
		view.Splits[i].Fastest = view.Splits[i].Label == view.FastestSplit
	}
	if firstSpeed > 0 {
		change := (lastSpeed/firstSpeed - 1) * 100
		view.FinishChange = fmt.Sprintf("%+.0f%%", change)
		view.FinishDetail = "Final quarter vs opening quarter, by elapsed speed"
	}
	if total >= fastFiveDistanceMeters {
		bestDuration := time.Duration(0)
		bestFrom := 0.0
		// Evaluate starts at every recorded distance and ends at every recorded
		// distance. The fastest window can have either kind of boundary.
		for _, point := range track {
			for _, from := range []float64{point.distance, point.distance - fastFiveDistanceMeters} {
				if from < 0 || from+fastFiveDistanceMeters > total {
					continue
				}
				start, okStart := pacingTimeAt(track, from)
				end, okEnd := pacingTimeAt(track, from+fastFiveDistanceMeters)
				if !okStart || !okEnd || !end.After(start) {
					continue
				}
				duration := end.Sub(start)
				if bestDuration == 0 || duration < bestDuration {
					bestDuration, bestFrom = duration, from
				}
			}
		}
		if bestDuration > 0 {
			view.FastestFiveKM = formatPacingDuration(bestDuration)
			view.FastestFiveFrom = fmt.Sprintf("From %.1f to %.1f km · %.1f km/h", bestFrom/1000, (bestFrom+fastFiveDistanceMeters)/1000, fastFiveDistanceMeters/bestDuration.Seconds()*3.6)
		}
	}
	return view
}

func buildPacingTrack(points []gps.Point) []pacingPoint {
	track := make([]pacingPoint, 0, len(points))
	var previous gps.Point
	for _, point := range points {
		if !validPacingCoordinate(point) || point.Time.IsZero() {
			continue
		}
		if len(track) > 0 && !point.Time.After(previous.Time) {
			continue
		}
		distance := 0.0
		if len(track) > 0 {
			seconds := point.Time.Sub(previous.Time).Seconds()
			edge := haversineMeters(previous.Lat, previous.Lon, point.Lat, point.Lon)
			if edge > maxPacingSpeedMPS*seconds+20 {
				continue
			}
			distance = track[len(track)-1].distance + edge
		}
		track = append(track, pacingPoint{
			distance: distance, time: point.Time, power: point.Power,
			heartRate: point.HeartRate, hasPower: point.HasPower,
			hasHeartRate: point.HasHeartRate,
		})
		previous = point
	}
	return track
}

func validPacingCoordinate(point gps.Point) bool {
	return !math.IsNaN(point.Lat) && !math.IsNaN(point.Lon) &&
		math.Abs(point.Lat) <= 90 && math.Abs(point.Lon) <= 180 &&
		(point.Lat != 0 || point.Lon != 0)
}

func pacingTimeAt(track []pacingPoint, distance float64) (time.Time, bool) {
	if len(track) == 0 || distance < 0 || distance > track[len(track)-1].distance {
		return time.Time{}, false
	}
	if distance == 0 {
		return track[0].time, true
	}
	i := sort.Search(len(track), func(i int) bool { return track[i].distance >= distance })
	if i == 0 || i >= len(track) {
		return time.Time{}, false
	}
	if track[i].distance == track[i-1].distance {
		return time.Time{}, false
	}
	fraction := (distance - track[i-1].distance) / (track[i].distance - track[i-1].distance)
	return track[i-1].time.Add(time.Duration(fraction * float64(track[i].time.Sub(track[i-1].time)))), true
}

func pacingSensorAverages(track []pacingPoint, start, end time.Time) (float64, bool, float64, bool) {
	var powerSum, powerSeconds, heartRateSum, heartRateSeconds float64
	for i := 1; i < len(track); i++ {
		// Do not carry one sensor sample across a long recording gap.
		if track[i].time.Sub(track[i-1].time) > 30*time.Second {
			continue
		}
		from, to := track[i-1].time, track[i].time
		if from.Before(start) {
			from = start
		}
		if to.After(end) {
			to = end
		}
		if !to.After(from) {
			continue
		}
		seconds := to.Sub(from).Seconds()
		if track[i-1].hasPower {
			powerSum += track[i-1].power * seconds
			powerSeconds += seconds
		}
		if track[i-1].hasHeartRate && track[i-1].heartRate > 0 {
			heartRateSum += track[i-1].heartRate * seconds
			heartRateSeconds += seconds
		}
	}
	// A few isolated samples should not become a split average.
	minCoverage := end.Sub(start).Seconds() * 0.5
	return powerSum / math.Max(powerSeconds, 1), powerSeconds >= minCoverage,
		heartRateSum / math.Max(heartRateSeconds, 1), heartRateSeconds >= minCoverage
}

func formatPacingDuration(duration time.Duration) string {
	seconds := int(math.Round(duration.Seconds()))
	if seconds < 0 {
		seconds = 0
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
