package strava

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	BaseURL     string
	AccessToken string
	TokenSource TokenSource
	HTTPClient  *http.Client
}

type Activity struct {
	ID          int64
	Name        string
	Type        string
	StartDate   time.Time
	Description string
}

type StreamSet struct {
	LatLng         [][2]float64
	TimeOffsetsSec []int
	VelocitySmooth []float64
}

func (c *Client) GetActivity(ctx context.Context, id int64) (Activity, error) {
	var payload struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		StartDate   string `json:"start_date"`
		Description string `json:"description"`
	}

	if err := c.getJSON(ctx, fmt.Sprintf("/activities/%d", id), nil, &payload); err != nil {
		return Activity{}, err
	}

	start, err := time.Parse(time.RFC3339, payload.StartDate)
	if err != nil {
		return Activity{}, fmt.Errorf("parse start_date: %w", err)
	}

	return Activity{
		ID:          payload.ID,
		Name:        payload.Name,
		Type:        payload.Type,
		StartDate:   start,
		Description: payload.Description,
	}, nil
}

func (c *Client) GetStreams(ctx context.Context, id int64) (StreamSet, error) {
	params := url.Values{}
	params.Set("keys", "latlng,time,velocity_smooth")
	params.Set("key_by_type", "true")

	var payload map[string]struct {
		Data []json.RawMessage `json:"data"`
	}

	if err := c.getJSON(ctx, fmt.Sprintf("/activities/%d/streams", id), params, &payload); err != nil {
		return StreamSet{}, err
	}

	var streams StreamSet
	for _, entry := range payload["latlng"].Data {
		var coords []float64
		if err := json.Unmarshal(entry, &coords); err != nil {
			return StreamSet{}, fmt.Errorf("parse latlng: %w", err)
		}
		if len(coords) != 2 {
			return StreamSet{}, fmt.Errorf("latlng entry has %d values", len(coords))
		}
		streams.LatLng = append(streams.LatLng, [2]float64{coords[0], coords[1]})
	}

	for _, entry := range payload["time"].Data {
		var v int
		if err := json.Unmarshal(entry, &v); err != nil {
			return StreamSet{}, fmt.Errorf("parse time: %w", err)
		}
		streams.TimeOffsetsSec = append(streams.TimeOffsetsSec, v)
	}

	for _, entry := range payload["velocity_smooth"].Data {
		var v float64
		if err := json.Unmarshal(entry, &v); err != nil {
			return StreamSet{}, fmt.Errorf("parse velocity_smooth: %w", err)
		}
		streams.VelocitySmooth = append(streams.VelocitySmooth, v)
	}

	return streams, nil
}

func (c *Client) getJSON(ctx context.Context, path string, params url.Values, target interface{}) error {
	base := c.BaseURL
	if base == "" {
		base = "https://www.strava.com/api/v3"
	}

	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	joined, err := url.JoinPath(u.Path, path)
	if err != nil {
		return err
	}
	u.Path = joined
	if params != nil {
		u.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	token := c.AccessToken
	if token == "" && c.TokenSource != nil {
		token, err = c.TokenSource.GetAccessToken(ctx)
		if err != nil {
			return err
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("strava error %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return err
	}

	return nil
}
