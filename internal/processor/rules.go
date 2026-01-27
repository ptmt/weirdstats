package processor

import (
	"context"
	"database/sql"

	"weirdstats/internal/rules"
	"weirdstats/internal/storage"
)

type RulesProcessor struct {
	Store    *storage.Store
	Registry rules.Registry
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

	return p.Store.UpdateActivityHiddenByRule(ctx, activityID, hide)
}
