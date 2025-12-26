package worker

import (
	"context"
	"database/sql"

	"weirdstats/internal/storage"
)

type Processor interface {
	Process(ctx context.Context, activityID int64) error
}

type Worker struct {
	Store     *storage.Store
	Processor Processor
}

func (w *Worker) ProcessNext(ctx context.Context) (bool, error) {
	queueID, activityID, err := w.Store.DequeueActivity(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	if err := w.Processor.Process(ctx, activityID); err != nil {
		return false, err
	}

	if err := w.Store.MarkProcessed(ctx, queueID); err != nil {
		return false, err
	}

	return true, nil
}
