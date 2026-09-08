package processor

import (
	"context"

	"weirdstats/internal/ingest"
)

type PipelineProcessor struct {
	SeparateStages bool
	Ingest         *ingest.Ingestor
	Stats          *StopStatsProcessor
	Rules          *RulesProcessor
	Applier        ActivityApplier
}

type ActivityApplier interface {
	Apply(ctx context.Context, activityID int64) error
}

func (p *PipelineProcessor) Process(ctx context.Context, activityID int64) error {
	if p.Ingest != nil {
		if err := p.Ingest.EnsureActivity(ctx, activityID); err != nil {
			return err
		}
	}
	if p.Stats != nil {
		statsProcessor := p.Stats
		if p.SeparateStages {
			copy := *p.Stats
			copy.MapAPI = nil
			copy.Overpass = nil
			statsProcessor = &copy
		}
		if err := statsProcessor.Process(ctx, activityID); err != nil {
			return err
		}
	}
	if p.SeparateStages {
		return nil
	}
	if p.Applier != nil {
		return p.Applier.Apply(ctx, activityID)
	}
	if p.Rules != nil {
		if err := p.Rules.Process(ctx, activityID); err != nil {
			return err
		}
	}
	return nil
}
