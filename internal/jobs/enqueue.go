package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"weirdstats/internal/storage"
)

func EnqueueProcessActivity(ctx context.Context, store *storage.Store, activityID, userID int64, publish ...bool) error {
	if store == nil {
		return fmt.Errorf("job store not configured")
	}
	if len(publish) == 0 || !publish[0] {
		return store.EnqueueActivity(ctx, activityID, userID)
	}
	payload, _ := json.Marshal(ProcessActivityPayload{ActivityID: activityID, UserID: userID, Publish: true})
	_, err := store.CreateJob(ctx, storage.Job{Type: JobTypeProcessActivity, Payload: string(payload), MaxAttempts: 10})
	return err
}

func EnqueueApplyActivityRules(ctx context.Context, store *storage.Store, activityID, userID int64, automatic ...bool) error {
	if store == nil {
		return fmt.Errorf("job store not configured")
	}
	payload := ProcessActivityPayload{ActivityID: activityID, UserID: userID, Publish: len(automatic) > 0 && automatic[0]}
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
