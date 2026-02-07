package stats

import "time"

type StopStats struct {
	StopCount             int
	StopTotalSeconds      int
	TrafficLightStopCount int
	EffortScore           float64
	EffortVersion         int
	UpdatedAt             time.Time
}
