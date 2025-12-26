package strava

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetsActivityAndStreams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/activities/123":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("missing auth header")
			}
			_, _ = w.Write([]byte(`{"id":123,"name":"Test Ride","type":"Ride","start_date":"2024-01-01T10:00:00Z","description":"desc"}`))
		case "/api/activities/123/streams":
			_, _ = w.Write([]byte(`{
  "latlng":{"data":[[1.0,2.0],[3.0,4.0]]},
  "time":{"data":[0,60]},
  "velocity_smooth":{"data":[1.2,2.3]}
}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL + "/api", AccessToken: "token"}

	activity, err := client.GetActivity(context.Background(), 123)
	if err != nil {
		t.Fatalf("get activity: %v", err)
	}
	if activity.Name != "Test Ride" {
		t.Fatalf("unexpected activity name: %s", activity.Name)
	}

	streams, err := client.GetStreams(context.Background(), 123)
	if err != nil {
		t.Fatalf("get streams: %v", err)
	}
	if len(streams.LatLng) != 2 || len(streams.TimeOffsetsSec) != 2 {
		t.Fatalf("unexpected stream lengths")
	}
}
