package jobs

import (
	"context"
	"fmt"

	"weirdstats/internal/storage"
)

func EnqueueProcessActivity(ctx context.Context, store *storage.Store, activityID int64) error {
	if store == nil {
		return fmt.Errorf("job store not configured")
	}
	return store.EnqueueActivity(ctx, activityID)
}
