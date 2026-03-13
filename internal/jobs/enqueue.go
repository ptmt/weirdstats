package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"weirdstats/internal/storage"
)

func EnqueueProcessActivity(ctx context.Context, store *storage.Store, activityID int64) error {
	if store == nil {
		return fmt.Errorf("job store not configured")
	}
	return store.EnqueueActivity(ctx, activityID)
}

func EnqueueApplyActivityRules(ctx context.Context, store *storage.Store, activityID int64) error {
	if store == nil {
		return fmt.Errorf("job store not configured")
	}
	payload := ProcessActivityPayload{ActivityID: activityID}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cursorJSON, err := json.Marshal(struct{}{})
	if err != nil {
		return err
	}
	_, err = store.CreateJob(ctx, storage.Job{
		Type:        JobTypeApplyActivityRules,
		Payload:     string(payloadJSON),
		Cursor:      string(cursorJSON),
		MaxAttempts: 5,
		NextRunAt:   time.Now(),
	})
	return err
}
