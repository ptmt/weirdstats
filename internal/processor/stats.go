package processor

import (
	"context"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/maps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

type StopStatsProcessor struct {
	Store    *storage.Store
	MapAPI   maps.API
	Overpass *maps.OverpassClient
	Options  gps.StopOptions
}

func (p *StopStatsProcessor) Process(ctx context.Context, activityID int64) error {
	points, err := p.Store.LoadActivityPoints(ctx, activityID)
	if err != nil {
		return err
	}

	stops := gps.DetectStops(points, p.Options)
	updatedAt := time.Now()
	stats := stats.StopStats{StopCount: len(stops), UpdatedAt: updatedAt}
	activityStartTime := time.Time{}
	if len(points) > 0 {
		activityStartTime = points[0].Time
	}
	var stopRows []storage.ActivityStop
	for i, stop := range stops {
		hasLight := false
		hasCrossing := false
		crossingRoad := ""

		stats.StopTotalSeconds += int(stop.Duration.Seconds())
		if p.MapAPI != nil {
			features, err := p.MapAPI.NearbyFeatures(stop.Lat, stop.Lon)
			if err != nil {
				return err
			}
			for _, feature := range features {
				if feature.Type == maps.FeatureTrafficLight {
					stats.TrafficLightStopCount++
					hasLight = true
					break
				}
			}
		}

		if !hasLight && p.Overpass != nil {
			stopStartSeconds := stop.StartTime.Sub(activityStartTime).Seconds()
			stopEndIdx := gps.FindStopEndIndex(points, stopStartSeconds, p.Options.SpeedThreshold, 0)
			if stopEndIdx >= 0 {
				roads, err := p.Overpass.FetchNearbyRoads(ctx, stop.Lat, stop.Lon, 30)
				if err != nil {
					return err
				}
				if len(roads) > 0 {
					result := gps.DetectRoadCrossing(points, stopEndIdx, roads)
					if result.Crossed {
						hasCrossing = true
						crossingRoad = result.RoadName
					}
				}
			}
		}

		stopRows = append(stopRows, storage.ActivityStop{
			Seq:             i,
			Lat:             stop.Lat,
			Lon:             stop.Lon,
			StartSeconds:    stop.StartTime.Sub(activityStartTime).Seconds(),
			DurationSeconds: int(stop.Duration.Seconds()),
			HasTrafficLight: hasLight,
			HasRoadCrossing: hasCrossing,
			CrossingRoad:    crossingRoad,
		})
	}

	if err := p.Store.UpsertActivityStats(ctx, activityID, stats); err != nil {
		return err
	}
	return p.Store.ReplaceActivityStops(ctx, activityID, stopRows, updatedAt)
}
