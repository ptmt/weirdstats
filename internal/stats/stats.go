package stats

import "time"

type StopStats struct {
	StopCount             int
	StopTotalSeconds      int
	TrafficLightStopCount int
	RoadCrossingCount     int
	EffortScore           float64
	EffortVersion         int
	UpdatedAt             time.Time
}
