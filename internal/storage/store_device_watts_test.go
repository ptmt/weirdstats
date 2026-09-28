package storage

import (
	"context"
	"testing"
	"time"
)

func TestActivityDeviceWattsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}

	meter := true
	activity := Activity{
		ID: 42, UserID: 7, Type: "Ride", Name: "TTT",
		StartTime:    time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC),
		DeviceWatts:  &meter,
		AthleteCount: 4,
	}
	if _, err := store.InsertActivity(ctx, activity, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetActivityForUser(ctx, 7, 42)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceWatts == nil || !*loaded.DeviceWatts {
		t.Fatalf("expected meter flag, got %+v", loaded.DeviceWatts)
	}
	if loaded.AthleteCount != 4 {
		t.Fatalf("expected group count 4, got %d", loaded.AthleteCount)
	}

	meter = false
	activity.DeviceWatts = &meter
	if _, err := store.UpsertActivity(ctx, activity, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetActivity(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceWatts == nil || *loaded.DeviceWatts {
		t.Fatalf("expected estimated power flag, got %+v", loaded.DeviceWatts)
	}

	activity.DeviceWatts = nil
	if _, err := store.UpsertActivity(ctx, activity, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetActivity(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceWatts != nil {
		t.Fatalf("expected unknown power source, got %+v", loaded.DeviceWatts)
	}
}

func TestActivityPowerMetadataMigratesExistingDatabase(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.db.ExecContext(ctx, `CREATE TABLE activities (
		id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, type TEXT NOT NULL,
		name TEXT NOT NULL, start_time INTEGER NOT NULL, description TEXT NOT NULL,
		distance REAL NOT NULL DEFAULT 0, moving_time INTEGER NOT NULL DEFAULT 0,
		average_power REAL NOT NULL DEFAULT 0, average_heartrate REAL NOT NULL DEFAULT 0,
		visibility TEXT NOT NULL DEFAULT '', is_private INTEGER NOT NULL DEFAULT 0,
		hide_from_home INTEGER NOT NULL DEFAULT 0, hidden_by_rule INTEGER NOT NULL DEFAULT 0,
		photo_url TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}

	meter := true
	activity := Activity{ID: 43, UserID: 7, Type: "Ride", Name: "TTT", StartTime: time.Now(), DeviceWatts: &meter, AthleteCount: 4}
	if _, err := store.InsertActivity(ctx, activity, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetActivity(ctx, 43)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceWatts == nil || !*loaded.DeviceWatts || loaded.AthleteCount != 4 {
		t.Fatalf("metadata missing after migration: %+v", loaded)
	}
}
