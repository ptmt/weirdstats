package processor

import (
	"context"
	"errors"
	"testing"
	"time"

	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

type stubActivityUpdater struct {
	calls          int
	lastActivityID int64
	lastRequest    strava.UpdateActivityRequest
	err            error
}

func (s *stubActivityUpdater) UpdateActivity(_ context.Context, id int64, update strava.UpdateActivityRequest) (strava.Activity, error) {
	s.calls++
	s.lastActivityID = id
	s.lastRequest = update
	if s.err != nil {
		return strava.Activity{}, s.err
	}

	hideFromHome := false
	if update.HideFromHome != nil {
		hideFromHome = *update.HideFromHome
	}

	return strava.Activity{
		ID:           id,
		HideFromHome: hideFromHome,
	}, nil
}

func openRulesStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return store
}

func insertActivityForRulesTest(t *testing.T, store *storage.Store, activityType string, hideFromHome bool) int64 {
	t.Helper()
	activityID, err := store.InsertActivity(context.Background(), storage.Activity{
		UserID:       1,
		Type:         activityType,
		Name:         "Rules test",
		StartTime:    time.Now().UTC(),
		HideFromHome: hideFromHome,
	}, nil)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	return activityID
}

func insertRideHideRule(t *testing.T, store *storage.Store) {
	t.Helper()
	_, err := store.CreateHideRule(context.Background(), storage.HideRule{
		UserID:    1,
		Name:      "Hide rides",
		Enabled:   true,
		Condition: `{"match":"all","conditions":[{"metric":"activity_type","op":"eq","values":["Ride"]}],"action":{"type":"hide"}}`,
	})
	if err != nil {
		t.Fatalf("create hide rule: %v", err)
	}
}

func TestRulesProcessorSyncsHideToStrava(t *testing.T) {
	ctx := context.Background()
	store := openRulesStore(t)
	insertRideHideRule(t, store)
	activityID := insertActivityForRulesTest(t, store, "Ride", false)

	updater := &stubActivityUpdater{}
	processor := &RulesProcessor{Store: store, Strava: updater}

	if err := processor.Process(ctx, activityID); err != nil {
		t.Fatalf("process: %v", err)
	}
	if updater.calls != 1 {
		t.Fatalf("expected 1 Strava update call, got %d", updater.calls)
	}
	if updater.lastActivityID != activityID {
		t.Fatalf("expected activity id %d, got %d", activityID, updater.lastActivityID)
	}
	if updater.lastRequest.HideFromHome == nil || !*updater.lastRequest.HideFromHome {
		t.Fatalf("expected hide_from_home=true update")
	}

	activity, err := store.GetActivity(ctx, activityID)
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if !activity.HiddenByRule {
		t.Fatalf("expected hidden_by_rule to be true")
	}
	if !activity.HideFromHome {
		t.Fatalf("expected hide_from_home to be true locally")
	}
}

func TestRulesProcessorDoesNotSyncWhenRuleDoesNotHide(t *testing.T) {
	ctx := context.Background()
	store := openRulesStore(t)
	insertRideHideRule(t, store)
	activityID := insertActivityForRulesTest(t, store, "Run", false)

	updater := &stubActivityUpdater{}
	processor := &RulesProcessor{Store: store, Strava: updater}

	if err := processor.Process(ctx, activityID); err != nil {
		t.Fatalf("process: %v", err)
	}
	if updater.calls != 0 {
		t.Fatalf("expected no Strava update calls, got %d", updater.calls)
	}

	activity, err := store.GetActivity(ctx, activityID)
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if activity.HiddenByRule {
		t.Fatalf("expected hidden_by_rule to be false")
	}
	if activity.HideFromHome {
		t.Fatalf("expected hide_from_home to remain false")
	}
}

func TestRulesProcessorDoesNotSyncWhenAlreadyHiddenOnStrava(t *testing.T) {
	ctx := context.Background()
	store := openRulesStore(t)
	insertRideHideRule(t, store)
	activityID := insertActivityForRulesTest(t, store, "Ride", true)

	updater := &stubActivityUpdater{}
	processor := &RulesProcessor{Store: store, Strava: updater}

	if err := processor.Process(ctx, activityID); err != nil {
		t.Fatalf("process: %v", err)
	}
	if updater.calls != 0 {
		t.Fatalf("expected no Strava update calls, got %d", updater.calls)
	}

	activity, err := store.GetActivity(ctx, activityID)
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if !activity.HiddenByRule {
		t.Fatalf("expected hidden_by_rule to be true")
	}
	if !activity.HideFromHome {
		t.Fatalf("expected hide_from_home to remain true")
	}
}

func TestRulesProcessorIgnoresStravaSyncError(t *testing.T) {
	ctx := context.Background()
	store := openRulesStore(t)
	insertRideHideRule(t, store)
	activityID := insertActivityForRulesTest(t, store, "Ride", false)

	updater := &stubActivityUpdater{err: errors.New("boom")}
	processor := &RulesProcessor{Store: store, Strava: updater}

	if err := processor.Process(ctx, activityID); err != nil {
		t.Fatalf("expected no error on strava sync failure, got %v", err)
	}
	if updater.calls != 1 {
		t.Fatalf("expected 1 Strava update call, got %d", updater.calls)
	}

	activity, err := store.GetActivity(ctx, activityID)
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if !activity.HiddenByRule {
		t.Fatalf("expected hidden_by_rule to be true")
	}
	if activity.HideFromHome {
		t.Fatalf("expected hide_from_home to stay false when Strava update fails")
	}
}
