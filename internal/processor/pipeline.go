package processor

import (
	"context"

	"weirdstats/internal/ingest"
)

type PipelineProcessor struct {
	Ingest  *ingest.Ingestor
	Stats   *StopStatsProcessor
	Rules   *RulesProcessor
	Applier ActivityApplier
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
		if err := p.Stats.Process(ctx, activityID); err != nil {
			return err
		}
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
