package gps

import "time"

type Point struct {
	Lat      float64
	Lon      float64
	Time     time.Time
	Speed    float64
	Power    float64
	HasPower bool
	Grade    float64
	HasGrade bool
}

type Stop struct {
	Lat       float64
	Lon       float64
	StartTime time.Time
	Duration  time.Duration
}

type StopOptions struct {
	SpeedThreshold  float64
	MinDuration     time.Duration
	GlitchTolerance time.Duration // ignore brief speed spikes shorter than this during a stop
}

func DetectStops(points []Point, opts StopOptions) []Stop {
	if len(points) == 0 {
		return nil
	}

	var stops []Stop
	var inStop bool
	var stopStart Point
	var lastSlow Point        // last point at or below threshold
	var glitchStart time.Time // when the current above-threshold glitch began

	for i, p := range points {
		slow := p.Speed <= opts.SpeedThreshold

		if slow {
			if !inStop {
				inStop = true
				stopStart = p
			}
			lastSlow = p
			glitchStart = time.Time{}
		} else if inStop {
			// Above threshold while in a stop — might be a glitch.
			if glitchStart.IsZero() {
				glitchStart = p.Time
			}
			if opts.GlitchTolerance > 0 && p.Time.Sub(glitchStart) < opts.GlitchTolerance {
				// Still within tolerance, stay in stop.
				continue
			}
			// Glitch exceeded tolerance (or no tolerance set): end the stop.
			duration := lastSlow.Time.Sub(stopStart.Time)
			if duration >= opts.MinDuration {
				stops = append(stops, Stop{
					Lat:       stopStart.Lat,
					Lon:       stopStart.Lon,
					StartTime: stopStart.Time,
					Duration:  duration,
				})
			}
			inStop = false
			glitchStart = time.Time{}
		}

		_ = i
	}

	if inStop {
		duration := lastSlow.Time.Sub(stopStart.Time)
		if duration >= opts.MinDuration {
			stops = append(stops, Stop{
				Lat:       stopStart.Lat,
				Lon:       stopStart.Lon,
				StartTime: stopStart.Time,
				Duration:  duration,
			})
		}
	}

	return stops
}
