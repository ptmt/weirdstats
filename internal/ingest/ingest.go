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
	Store   *storage.Store
	Strava  *strava.Client
	Clients *strava.ClientFactory
}

func (i *Ingestor) EnsureActivity(ctx context.Context, activityID int64) error {
	userID := UserIDFromContext(ctx)
	exists, err := i.Store.HasActivity(ctx, activityID)
	if err != nil {
		return err
	}

	if exists {
		activity, err := i.Store.GetActivity(ctx, activityID)
		if err != nil {
			return err
		}
		userID = activity.UserID
	} else if userID == 0 {
		return fmt.Errorf("activity %d user unknown", activityID)
	}

	if !exists {
		return i.fetchAndUpsert(ctx, userID, activityID)
	}

	count, err := i.Store.CountActivityPoints(ctx, activityID)
	if err != nil {
		return err
	}
	if count == 0 {
		return i.fetchAndUpsert(ctx, userID, activityID)
	}

	return nil
}

func (i *Ingestor) fetchAndUpsert(ctx context.Context, userID, activityID int64) error {
	client, err := i.clientForUser(ctx, userID)
	if err != nil {
		return err
	}

	activity, err := client.GetActivity(ctx, activityID)
	if err != nil {
		return err
	}

	streams, err := client.GetStreams(ctx, activityID)
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
		UserID:           userID,
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

func (i *Ingestor) SyncLatestActivity(ctx context.Context, userID int64) (int, error) {
	client, err := i.clientForUser(ctx, userID)
	if err != nil {
		return 0, err
	}

	activities, err := client.ListActivities(ctx, time.Time{}, time.Time{}, 1, 1)
	if err != nil {
		return 0, err
	}

	if len(activities) == 0 {
		return 0, nil
	}

	if err := i.fetchAndUpsert(ctx, userID, activities[0].ID); err != nil {
		return 0, err
	}

	if err := i.Store.EnqueueActivity(ctx, activities[0].ID, userID); err != nil {
		return 0, err
	}

	return 1, nil
}

func (i *Ingestor) SyncActivitiesSince(ctx context.Context, userID int64, after time.Time) (int, error) {
	client, err := i.clientForUser(ctx, userID)
	if err != nil {
		return 0, err
	}

	var allActivities []strava.ActivitySummary
	page := 1
	perPage := 100

	for {
		activities, err := client.ListActivities(ctx, after, time.Time{}, page, perPage)
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
		if err := i.fetchAndUpsert(ctx, userID, activity.ID); err != nil {
			return synced, fmt.Errorf("activity %d: %w", activity.ID, err)
		}

		if err := i.Store.EnqueueActivity(ctx, activity.ID, userID); err != nil {
			return synced, fmt.Errorf("enqueue %d: %w", activity.ID, err)
		}

		synced++
	}

	return synced, nil
}

type userIDContextKey struct{}

func ContextWithUserID(ctx context.Context, userID int64) context.Context {
	if userID == 0 {
		return ctx
	}
	return context.WithValue(ctx, userIDContextKey{}, userID)
}

func UserIDFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	userID, _ := ctx.Value(userIDContextKey{}).(int64)
	return userID
}

func (i *Ingestor) clientForUser(ctx context.Context, userID int64) (*strava.Client, error) {
	if userID == 0 {
		return nil, fmt.Errorf("strava user id required")
	}
	if i.Clients != nil {
		return i.Clients.ClientForUser(ctx, userID)
	}
	if i.Strava != nil {
		return i.Strava, nil
	}
	return nil, fmt.Errorf("strava client not configured")
}

func (i *Ingestor) ClientForUser(ctx context.Context, userID int64) (*strava.Client, error) {
	return i.clientForUser(ctx, userID)
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
		if idx < len(streams.Watts) {
			p.Power = streams.Watts[idx]
			p.HasPower = true
		}
		if idx < len(streams.GradeSmooth) {
			p.Grade = streams.GradeSmooth[idx]
			p.HasGrade = true
		}
		points = append(points, p)
	}
	return points, nil
}
