package stats

import "time"

type StopStats struct {
	StopCount             int
	StopTotalSeconds      int
	TrafficLightStopCount int
	UpdatedAt             time.Time
}
