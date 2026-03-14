package processor

import (
	"context"
	"database/sql"
	"log"

	"weirdstats/internal/rules"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

type ActivityUpdater interface {
	UpdateActivity(ctx context.Context, id int64, update strava.UpdateActivityRequest) (strava.Activity, error)
}

type RulesProcessor struct {
	Store    *storage.Store
	Registry rules.Registry
	Strava   ActivityUpdater
	Clients  *strava.ClientFactory
}

func (p *RulesProcessor) Process(ctx context.Context, activityID int64) error {
	if p.Store == nil {
		return nil
	}
	activity, err := p.Store.GetActivity(ctx, activityID)
	if err != nil {
		return err
	}
	ruleRows, err := p.Store.ListHideRules(ctx, activity.UserID)
	if err != nil {
		return err
	}
	stats, err := p.Store.GetActivityStats(ctx, activityID)
	if err != nil {
		if err != sql.ErrNoRows {
			return err
		}
	}
	reg := p.Registry
	if reg == nil {
		reg = rules.DefaultRegistry()
	}
	startUnix := int64(0)
	if !activity.StartTime.IsZero() {
		startUnix = activity.StartTime.Unix()
	}
	ctxData := rules.Context{
		Activity: rules.ActivitySource{
			ID:          activity.ID,
			Type:        activity.Type,
			Name:        activity.Name,
			StartUnix:   startUnix,
			DistanceM:   activity.Distance,
			MovingTimeS: activity.MovingTime,
		},
		Stats: rules.StatsSource{
			StopCount:             stats.StopCount,
			StopTotalSeconds:      stats.StopTotalSeconds,
			TrafficLightStopCount: stats.TrafficLightStopCount,
		},
	}

	hide := false
	for _, ruleRow := range ruleRows {
		if !ruleRow.Enabled {
			continue
		}
		ruleDef, err := rules.ParseRuleJSON(ruleRow.Condition)
		if err != nil {
			continue
		}
		if err := rules.ValidateRule(ruleDef, reg); err != nil {
			continue
		}
		matched, shouldHide, err := rules.Evaluate(ruleDef, reg, ctxData, ruleRow.ID)
		if err != nil {
			continue
		}
		if matched && shouldHide {
			hide = true
			break
		}
	}

	if err := p.Store.UpdateActivityHiddenByRule(ctx, activityID, hide); err != nil {
		return err
	}

	if !hide || activity.HideFromHome {
		return nil
	}

	updater, err := p.activityUpdater(ctx, activity.UserID)
	if err != nil {
		log.Printf("rules processor: strava updater unavailable for user %d: %v", activity.UserID, err)
		return nil
	}

	hideFromHome := true
	if _, err := updater.UpdateActivity(ctx, activityID, strava.UpdateActivityRequest{
		HideFromHome: &hideFromHome,
	}); err != nil {
		// Keep processing moving even if Strava update fails; activity can be retried manually.
		log.Printf("rules processor: strava hide sync failed for activity %d: %v", activityID, err)
		return nil
	}

	return p.Store.UpdateActivityHideFromHome(ctx, activityID, true)
}

func (p *RulesProcessor) activityUpdater(ctx context.Context, userID int64) (ActivityUpdater, error) {
	if p.Strava != nil {
		return p.Strava, nil
	}
	if p.Clients == nil {
		return nil, sql.ErrNoRows
	}
	return p.Clients.ClientForUser(ctx, userID)
}
