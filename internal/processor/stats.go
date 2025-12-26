package processor

import (
	"context"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

type StopStatsProcessor struct {
	Store   *storage.Store
	MapAPI  maps.API
	Options gps.StopOptions
}

func (p *StopStatsProcessor) Process(ctx context.Context, activityID int64) error {
	points, err := p.Store.LoadActivityPoints(ctx, activityID)
	if err != nil {
		return err
	}

	stops := gps.DetectStops(points, p.Options)
	stats := stats.StopStats{StopCount: len(stops)}
	for _, stop := range stops {
		stats.StopTotalSeconds += int(stop.Duration.Seconds())
		if p.MapAPI == nil {
			continue
		}
		features, err := p.MapAPI.NearbyFeatures(stop.Lat, stop.Lon)
		if err != nil {
			return err
		}
		for _, feature := range features {
			if feature.Type == maps.FeatureTrafficLight {
				stats.TrafficLightStopCount++
				break
			}
		}
	}

	return p.Store.UpsertActivityStats(ctx, activityID, stats)
}
