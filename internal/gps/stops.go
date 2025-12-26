package gps

import "time"

type Point struct {
	Lat   float64
	Lon   float64
	Time  time.Time
	Speed float64
}

type Stop struct {
	Lat      float64
	Lon      float64
	Duration time.Duration
}

type StopOptions struct {
	SpeedThreshold float64
	MinDuration    time.Duration
}

func DetectStops(points []Point, opts StopOptions) []Stop {
	if len(points) == 0 {
		return nil
	}

	var stops []Stop
	var inStop bool
	var stopStart Point
	var last Point

	for i, p := range points {
		if i == 0 {
			last = p
		}

		if p.Speed <= opts.SpeedThreshold {
			if !inStop {
				inStop = true
				stopStart = p
			}
		} else if inStop {
			duration := last.Time.Sub(stopStart.Time)
			if duration >= opts.MinDuration {
				stops = append(stops, Stop{
					Lat:      stopStart.Lat,
					Lon:      stopStart.Lon,
					Duration: duration,
				})
			}
			inStop = false
		}

		last = p
	}

	if inStop {
		duration := last.Time.Sub(stopStart.Time)
		if duration >= opts.MinDuration {
			stops = append(stops, Stop{
				Lat:      stopStart.Lat,
				Lon:      stopStart.Lon,
				Duration: duration,
			})
		}
	}

	return stops
}
