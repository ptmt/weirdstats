package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"weirdstats/internal/storage"
)

func parseActivityCursor(raw string) (storage.ActivityCursor, error) {
	var cursor storage.ActivityCursor
	if raw == "" {
		return cursor, nil
	}
	if len(raw) > 200 {
		return cursor, fmt.Errorf("invalid activity cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	if err = json.Unmarshal(data, &cursor); err != nil {
		return cursor, err
	}
	if cursor.ID <= 0 {
		return cursor, fmt.Errorf("invalid activity cursor")
	}
	return cursor, nil
}

func nextActivityCursor(activity storage.Activity) string {
	data, _ := json.Marshal(storage.ActivityCursor{StartUnix: activity.StartTime.Unix(), ID: activity.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}
