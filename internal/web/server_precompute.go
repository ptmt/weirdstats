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

	local := *s
	local.overpass = nil
	hide, _, events, err := local.evaluateHideRulesWithHistory(ctx, activity)
	if err != nil {
		return err
	}
	if err := s.store.UpdateActivityHiddenByRule(ctx, activity.ID, hide); err != nil {
		return err
	}
	if err := local.updateActivityDetectedFactsCache(
		ctx,
		activity,
		statsSnapshot,
		points,
		stops,
		rideSegmentFact{},
		nil,
		heartRateChangeFact{},
		coffeeStopFact{},
		routeHighlightFact{},
		roadCrossingFact{},
	); err != nil {
		return err
	}
	prefs, err := s.store.ProcessingPreferences(ctx, activity.UserID)
	if err != nil {
		return err
	}
	for i := range events {
		events[i].Source, events[i].Triggered = "analysis", false
		if events[i].Status == "matched" {
			if prefs.AutoPublish {
				events[i].Detail += ". Matched during analysis; Strava publishing runs separately."
			} else {
				events[i].Status = "disabled"
				events[i].Detail += ". Matched, but automatic Strava updates are off."
			}
		}
	}
	if !prefs.AutoPublish {
		events = append(events, storage.AutomationEvent{Name: "Automatic Strava updates", Action: "publish", Source: "analysis", Status: "disabled", Detail: "Activity analysis is available. Automatic Strava updates are off in Settings."})
	}
	return s.store.RecordAutomationEvents(ctx, activity.UserID, activity.ID, events)
}
