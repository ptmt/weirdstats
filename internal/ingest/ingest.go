package ingest

import (
	"context"
	"fmt"
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

	_, err = i.Store.UpsertActivity(ctx, storage.Activity{
		ID:          activity.ID,
		UserID:      0,
		Type:        activity.Type,
		Name:        activity.Name,
		StartTime:   activity.StartDate,
		Description: activity.Description,
	}, points)
	return err
}

func buildPoints(start time.Time, streams strava.StreamSet) ([]gps.Point, error) {
	if len(streams.LatLng) == 0 {
		return nil, fmt.Errorf("missing latlng stream")
	}
	if len(streams.TimeOffsetsSec) == 0 {
		return nil, fmt.Errorf("missing time stream")
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
