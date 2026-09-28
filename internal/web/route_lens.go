package web

import (
	"math"
	"sort"

	"weirdstats/internal/gps"
)

var routeSpeedPalette = [...]string{"#377cfb", "#19b9d2", "#76c866", "#f4b544", "#f06b3f", "#df3974"}

const routeStoppedColor = "#9ca3af"

type routeSpeedStyle struct {
	Colors    []string // Color of the segment ending at point i+1.
	SlowKmh   int
	FastKmh   int
	Available bool
}

func buildRouteSpeedStyle(points []gps.Point, stopThreshold float64) routeSpeedStyle {
	style := routeSpeedStyle{Colors: make([]string, max(0, len(points)-1))}
	if len(points) < 2 {
		return style
	}
	if stopThreshold <= 0 {
		stopThreshold = 0.5
	}

	// A short median window removes isolated GPS spikes without washing out a
	// sustained faster section. Ignore clearly invalid recorded speeds.
	smoothed := make([]float64, len(points))
	for i := range points {
		window := make([]float64, 0, 5)
		for j := max(0, i-2); j <= min(len(points)-1, i+2); j++ {
			speed := points[j].Speed
			if !math.IsNaN(speed) && !math.IsInf(speed, 0) && speed >= 0 && speed <= 50 {
				window = append(window, speed)
			}
		}
		if len(window) > 0 {
			sort.Float64s(window)
			smoothed[i] = window[len(window)/2]
		}
	}

	moving := make([]float64, 0, len(points))
	for _, speed := range smoothed {
		if speed > stopThreshold {
			moving = append(moving, speed)
		}
	}
	if len(moving) == 0 {
		for i := range style.Colors {
			style.Colors[i] = routeStoppedColor
		}
		return style
	}
	sort.Float64s(moving)
	low := speedQuantile(moving, 0.1)
	high := speedQuantile(moving, 0.9)
	style.Available = true
	style.SlowKmh = int(math.Round(low * 3.6))
	style.FastKmh = int(math.Round(high * 3.6))
	for i := range style.Colors {
		speed := (smoothed[i] + smoothed[i+1]) / 2
		if speed <= stopThreshold {
			style.Colors[i] = routeStoppedColor
			continue
		}
		fraction := 0.5
		if high > low {
			fraction = math.Max(0, math.Min(1, (speed-low)/(high-low)))
		}
		index := min(len(routeSpeedPalette)-1, int(fraction*float64(len(routeSpeedPalette))))
		style.Colors[i] = routeSpeedPalette[index]
	}
	return style
}

func speedQuantile(sorted []float64, fraction float64) float64 {
	position := fraction * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	return sorted[lower] + (sorted[upper]-sorted[lower])*(position-float64(lower))
}
