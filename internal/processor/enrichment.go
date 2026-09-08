package processor

import "context"

type EnrichmentProcessor struct {
	Stats *StopStatsProcessor
	Facts interface {
		Enrich(context.Context, int64) error
	}
}

func (p *EnrichmentProcessor) Process(ctx context.Context, id int64) error {
	if err := p.Stats.Process(ctx, id); err != nil {
		return err
	}
	return p.Facts.Enrich(ctx, id)
}
