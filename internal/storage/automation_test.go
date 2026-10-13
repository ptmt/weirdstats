package storage

import (
	"context"
	"testing"
	"time"
)

func automationTestStore(t *testing.T) (*Store, int64) {
	t.Helper()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, err := store.InsertActivity(context.Background(), Activity{UserID: 11, Type: "VirtualRide", Name: "ROUVY workout", StartTime: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return store, id
}

func TestAutomationHistoryPaginationAndOwnership(t *testing.T) {
	store, id := automationTestStore(t)
	ctx := context.Background()
	events := []AutomationEvent{
		{Name: "Rule snapshot", RuleID: 7, Action: "hide", Source: "automatic", Status: "applied", Triggered: true},
		{Name: "Unmatched rule", Action: "hide", Source: "analysis", Status: "no_match"},
		{Name: "Description", Action: "description", Source: "manual", Status: "failed", Triggered: true},
	}
	if err := store.RecordAutomationEvents(ctx, 11, id, events); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAutomationEvents(ctx, 22, id, events); err == nil {
		t.Fatal("another user recorded history")
	}
	page, err := store.ListAutomationEvents(ctx, 11, id, 0, 2)
	if err != nil || len(page) != 2 || page[0].Name != "Description" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	next, err := store.ListAutomationEvents(ctx, 11, id, page[1].ID, 2)
	if err != nil || len(next) != 1 || next[0].Name != "Rule snapshot" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	other, err := store.ListAutomationEvents(ctx, 22, 0, 0, 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("another user can read history: %+v %v", other, err)
	}
	counts, err := store.CountTriggeredAutomations(ctx, 11, []int64{id})
	if err != nil || counts[id] != 2 {
		t.Fatalf("triggered counts=%v err=%v", counts, err)
	}
	counts, err = store.CountTriggeredAutomations(ctx, 22, []int64{id})
	if err != nil || len(counts) != 0 {
		t.Fatalf("another user can read counts: %v %v", counts, err)
	}
}

func TestAutomationHistorySurvivesRefreshAndLateCompletion(t *testing.T) {
	store, id := automationTestStore(t)
	ctx := context.Background()
	events := []AutomationEvent{{Name: "Hide workout", Action: "hide", Source: "automatic", Status: "pending", Triggered: true}}
	ids, err := store.StartAutomationEvents(ctx, 11, id, events)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InvalidateActivity(ctx, 11, id, false); err != nil {
		t.Fatal(err)
	}
	events[0].Status = "applied"
	if err := store.FinishAutomationEvents(ctx, 11, id, ids, events); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertActivity(ctx, Activity{ID: id, UserID: 11, Type: "VirtualRide", Name: "Updated workout", StartTime: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListAutomationEvents(ctx, 11, id, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].Status != "applied" {
		t.Fatalf("refresh lost history: %+v %v", rows, err)
	}
	if err := store.InvalidateActivity(ctx, 11, id, true); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAutomationEvents(ctx, 11, id, ids, events); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM automation_events").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("deleted history recreated: %d %v", remaining, err)
	}
	if err := store.RecordAutomationEvents(ctx, 11, id, events); err == nil {
		t.Fatal("history recreated a deleted activity")
	}
}

func TestAutomationHistoryPurgedWithUserData(t *testing.T) {
	for _, operation := range []string{"purge", "delete"} {
		t.Run(operation, func(t *testing.T) {
			store, id := automationTestStore(t)
			ctx := context.Background()
			if err := store.RecordAutomationEvents(ctx, 11, id, []AutomationEvent{{Name: "Hide", Action: "hide", Source: "automatic", Status: "applied", Triggered: true}}); err != nil {
				t.Fatal(err)
			}
			var err error
			if operation == "purge" {
				err = store.PurgeImportedData(ctx, 11)
			} else {
				err = store.DeleteUserData(ctx, 11)
			}
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM automation_events WHERE user_id=11").Scan(&count); err != nil || count != 0 {
				t.Fatalf("history retained: %d %v", count, err)
			}
		})
	}
}
