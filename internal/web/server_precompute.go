package web

import (
	"context"

	"weirdstats/internal/gps"
	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

// PrecomputeActivityFacts persists detected facts and fact metrics during processing
// so the activity UI does not have to wait for a later apply pass.
func (s *Server) PrecomputeActivityFacts(ctx context.Context, activity storage.Activity, statsSnapshot stats.StopStats, points []gps.Point, stops []storage.ActivityStop) error {
	if s == nil || s.store == nil {
		return nil
	}

	s.updateActivityDetectedFactsCache(
		ctx,
		activity,
		statsSnapshot,
		points,
		stops,
		rideSegmentFact{},
		nil,
		coffeeStopFact{},
		routeHighlightFact{},
		roadCrossingFact{},
	)
	return nil
}
