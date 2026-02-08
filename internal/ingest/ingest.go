package ingest

import (
	"context"
	"fmt"
	"log"
	"time"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
	"weirdstats/internal/strava"
)

type Ingestor struct {
	Store  *storage.Store
	Strava *strava.Client
}

func (i *Ingestor) EnsureActivity(ctx context.Context, activityID int64) error {
	exists, err := i.Store.HasActivity(ctx, activityID)
	if err != nil {
		return err
	}

	if !exists {
		return i.fetchAndUpsert(ctx, activityID)
	}

	count, err := i.Store.CountActivityPoints(ctx, activityID)
	if err != nil {
		return err
	}
	if count == 0 {
		return i.fetchAndUpsert(ctx, activityID)
	}

	return nil
}

func (i *Ingestor) fetchAndUpsert(ctx context.Context, activityID int64) error {
	if i.Strava == nil {
		return fmt.Errorf("strava client not configured")
	}

	activity, err := i.Strava.GetActivity(ctx, activityID)
	if err != nil {
		return err
	}

	streams, err := i.Strava.GetStreams(ctx, activityID)
	if err != nil {
		return err
	}

	points, err := buildPoints(activity.StartDate, streams)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		log.Printf("Activity %d (%s) has no GPS data", activity.ID, activity.Name)
	}

	_, err = i.Store.UpsertActivity(ctx, storage.Activity{
		ID:               activity.ID,
		UserID:           1,
		Type:             activity.Type,
		Name:             activity.Name,
		StartTime:        activity.StartDate,
		Description:      activity.Description,
		Distance:         activity.Distance,
		MovingTime:       activity.MovingTime,
		AveragePower:     activity.AveragePower,
		AverageHeartRate: activity.AverageHeartRate,
		Visibility:       activity.Visibility,
		IsPrivate:        activity.Private,
		HideFromHome:     activity.HideFromHome,
		PhotoURL:         activity.PhotoURL,
	}, points)
	return err
}

func (i *Ingestor) SyncLatestActivity(ctx context.Context) (int, error) {
	if i.Strava == nil {
		return 0, fmt.Errorf("strava client not configured")
	}

	activities, err := i.Strava.ListActivities(ctx, time.Time{}, time.Time{}, 1, 1)
	if err != nil {
		return 0, err
	}

	if len(activities) == 0 {
		return 0, nil
	}

	if err := i.fetchAndUpsert(ctx, activities[0].ID); err != nil {
		return 0, err
	}

	if err := i.Store.EnqueueActivity(ctx, activities[0].ID); err != nil {
		return 0, err
	}

	return 1, nil
}

func (i *Ingestor) SyncActivitiesSince(ctx context.Context, after time.Time) (int, error) {
	if i.Strava == nil {
		return 0, fmt.Errorf("strava client not configured")
	}

	var allActivities []strava.ActivitySummary
	page := 1
	perPage := 100

	for {
		activities, err := i.Strava.ListActivities(ctx, after, time.Time{}, page, perPage)
		if err != nil {
			return 0, err
		}

		if len(activities) == 0 {
			break
		}

		allActivities = append(allActivities, activities...)

		if len(activities) < perPage {
			break
		}
		page++
	}

	synced := 0
	for _, activity := range allActivities {
		if err := i.fetchAndUpsert(ctx, activity.ID); err != nil {
			return synced, fmt.Errorf("activity %d: %w", activity.ID, err)
		}

		if err := i.Store.EnqueueActivity(ctx, activity.ID); err != nil {
			return synced, fmt.Errorf("enqueue %d: %w", activity.ID, err)
		}

		synced++
	}

	return synced, nil
}

func buildPoints(start time.Time, streams strava.StreamSet) ([]gps.Point, error) {
	if len(streams.LatLng) == 0 {
		// No GPS data - indoor activity or manual entry
		return nil, nil
	}
	if len(streams.TimeOffsetsSec) == 0 {
		return nil, nil
	}
	if len(streams.LatLng) != len(streams.TimeOffsetsSec) {
		return nil, fmt.Errorf("latlng/time length mismatch")
	}

	points := make([]gps.Point, 0, len(streams.LatLng))
	for idx, coord := range streams.LatLng {
		p := gps.Point{
			Lat:  coord[0],
			Lon:  coord[1],
			Time: start.Add(time.Duration(streams.TimeOffsetsSec[idx]) * time.Second),
		}
		if idx < len(streams.VelocitySmooth) {
			p.Speed = streams.VelocitySmooth[idx]
		}
		points = append(points, p)
	}
	return points, nil
}
