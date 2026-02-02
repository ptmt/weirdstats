package strava

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL     string
	AccessToken string
	TokenSource TokenSource
	HTTPClient  *http.Client
}

type APIError struct {
	StatusCode int
	Body       string
	Method     string
	Path       string
	RequestID  string
	RateLimit  RateLimitInfo
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("strava error %d", e.StatusCode))
	if e.Method != "" || e.Path != "" {
		b.WriteString(" ")
		if e.Method != "" {
			b.WriteString(e.Method)
		}
		if e.Path != "" {
			if e.Method != "" {
				b.WriteString(" ")
			}
			b.WriteString(e.Path)
		}
	}
	if trimmed := strings.TrimSpace(e.Body); trimmed != "" {
		b.WriteString(": ")
		b.WriteString(trimmed)
	}
	if info := e.RateLimit.String(); info != "" {
		b.WriteString(" (")
		b.WriteString(info)
		b.WriteString(")")
	}
	if e.RequestID != "" {
		b.WriteString(" request_id=")
		b.WriteString(e.RequestID)
	}
	return b.String()
}

type RateLimitInfo struct {
	LimitShort    int
	LimitLong     int
	UsageShort    int
	UsageLong     int
	RetryAfter    time.Duration
	RetryAt       time.Time
	RetryAfterRaw string
}

func (r RateLimitInfo) HasData() bool {
	return r.LimitShort >= 0 || r.LimitLong >= 0 || r.UsageShort >= 0 || r.UsageLong >= 0 ||
		r.RetryAfter > 0 || !r.RetryAt.IsZero() || r.RetryAfterRaw != ""
}

func (r RateLimitInfo) String() string {
	if !r.HasData() {
		return ""
	}
	parts := []string{"rate-limit"}
	if r.UsageShort >= 0 || r.LimitShort >= 0 {
		parts = append(parts, fmt.Sprintf("short=%s", formatUsageLimit(r.UsageShort, r.LimitShort)))
	}
	if r.UsageLong >= 0 || r.LimitLong >= 0 {
		parts = append(parts, fmt.Sprintf("long=%s", formatUsageLimit(r.UsageLong, r.LimitLong)))
	}
	if r.RetryAfter > 0 {
		parts = append(parts, fmt.Sprintf("retry-after=%s", r.RetryAfter.Truncate(time.Second)))
	} else if !r.RetryAt.IsZero() {
		parts = append(parts, fmt.Sprintf("retry-at=%s", r.RetryAt.UTC().Format(time.RFC3339)))
	} else if r.RetryAfterRaw != "" {
		parts = append(parts, fmt.Sprintf("retry-after=%s", r.RetryAfterRaw))
	}
	return strings.Join(parts, " ")
}

func formatUsageLimit(usage, limit int) string {
	usageText := "?"
	limitText := "?"
	if usage >= 0 {
		usageText = strconv.Itoa(usage)
	}
	if limit >= 0 {
		limitText = strconv.Itoa(limit)
	}
	return fmt.Sprintf("%s/%s", usageText, limitText)
}

func parseRateLimitInfo(headers http.Header) RateLimitInfo {
	info := RateLimitInfo{
		LimitShort: -1,
		LimitLong:  -1,
		UsageShort: -1,
		UsageLong:  -1,
	}
	if limitHeader := headers.Get("X-RateLimit-Limit"); limitHeader != "" {
		info.LimitShort, info.LimitLong = parseRateLimitPair(limitHeader)
	}
	if usageHeader := headers.Get("X-RateLimit-Usage"); usageHeader != "" {
		info.UsageShort, info.UsageLong = parseRateLimitPair(usageHeader)
	}
	if retryAfter := strings.TrimSpace(headers.Get("Retry-After")); retryAfter != "" {
		info.RetryAfterRaw = retryAfter
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			info.RetryAfter = time.Duration(secs) * time.Second
		} else if retryAt, err := http.ParseTime(retryAfter); err == nil {
			info.RetryAt = retryAt
		}
	}
	return info
}

func parseRateLimitPair(value string) (int, int) {
	parts := strings.Split(value, ",")
	short := -1
	long := -1
	if len(parts) > 0 {
		short = parseRateLimitValue(parts[0])
	}
	if len(parts) > 1 {
		long = parseRateLimitValue(parts[1])
	}
	return short, long
}

func parseRateLimitValue(value string) int {
	v := strings.TrimSpace(value)
	if v == "" {
		return -1
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}

func newAPIError(resp *http.Response, req *http.Request, body []byte) *APIError {
	err := &APIError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
		RateLimit:  parseRateLimitInfo(resp.Header),
	}
	if req != nil {
		err.Method = req.Method
		if req.URL != nil {
			err.Path = req.URL.RequestURI()
		}
	}
	if requestID := resp.Header.Get("X-Request-Id"); requestID != "" {
		err.RequestID = requestID
	} else if requestID := resp.Header.Get("X-Request-ID"); requestID != "" {
		err.RequestID = requestID
	}
	return err
}

type Activity struct {
	ID           int64
	Name         string
	Type         string
	StartDate    time.Time
	Description  string
	Distance     float64
	MovingTime   int
	AveragePower float64
	Visibility   string
	Private      bool
	HideFromHome bool
}

type ActivitySummary struct {
	ID        int64
	Name      string
	Type      string
	StartDate time.Time
}

type StreamSet struct {
	LatLng         [][2]float64
	TimeOffsetsSec []int
	VelocitySmooth []float64
}

type UpdateActivityRequest struct {
	Description  *string
	HideFromHome *bool
}

func (c *Client) GetActivity(ctx context.Context, id int64) (Activity, error) {
	var payload struct {
		ID           int64   `json:"id"`
		Name         string  `json:"name"`
		Type         string  `json:"type"`
		StartDate    string  `json:"start_date"`
		Description  string  `json:"description"`
		Distance     float64 `json:"distance"`
		MovingTime   int     `json:"moving_time"`
		AverageWatts float64 `json:"average_watts"`
		Visibility   string  `json:"visibility"`
		Private      bool    `json:"private"`
		HideFromHome bool    `json:"hide_from_home"`
	}

	if err := c.getJSON(ctx, fmt.Sprintf("/activities/%d", id), nil, &payload); err != nil {
		return Activity{}, err
	}

	start, err := time.Parse(time.RFC3339, payload.StartDate)
	if err != nil {
		return Activity{}, fmt.Errorf("parse start_date: %w", err)
	}

	return Activity{
		ID:           payload.ID,
		Name:         payload.Name,
		Type:         payload.Type,
		StartDate:    start,
		Description:  payload.Description,
		Distance:     payload.Distance,
		MovingTime:   payload.MovingTime,
		AveragePower: payload.AverageWatts,
		Visibility:   payload.Visibility,
		Private:      payload.Private,
		HideFromHome: payload.HideFromHome,
	}, nil
}

func (c *Client) UpdateActivity(ctx context.Context, id int64, update UpdateActivityRequest) (Activity, error) {
	if id == 0 {
		return Activity{}, fmt.Errorf("activity id required")
	}
	form := url.Values{}
	if update.Description != nil {
		form.Set("description", *update.Description)
	}
	if update.HideFromHome != nil {
		form.Set("hide_from_home", strconv.FormatBool(*update.HideFromHome))
	}
	if len(form) == 0 {
		return Activity{}, fmt.Errorf("no updates specified")
	}

	base := c.BaseURL
	if base == "" {
		base = "https://www.strava.com/api/v3"
	}
	endpoint, err := url.JoinPath(base, "/activities", fmt.Sprintf("%d", id))
	if err != nil {
		return Activity{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Activity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	token := c.AccessToken
	if token == "" && c.TokenSource != nil {
		token, err = c.TokenSource.GetAccessToken(ctx)
		if err != nil {
			return Activity{}, err
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
		return Activity{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Activity{}, newAPIError(resp, req, body)
	}

	var payload struct {
		ID           int64  `json:"id"`
		Description  string `json:"description"`
		Visibility   string `json:"visibility"`
		Private      bool   `json:"private"`
		HideFromHome bool   `json:"hide_from_home"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Activity{}, err
	}

	return Activity{
		ID:           payload.ID,
		Description:  payload.Description,
		Visibility:   payload.Visibility,
		Private:      payload.Private,
		HideFromHome: payload.HideFromHome,
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

func (c *Client) ListActivities(ctx context.Context, after, before time.Time, page, perPage int) ([]ActivitySummary, error) {
	params := url.Values{}
	if !after.IsZero() {
		params.Set("after", fmt.Sprintf("%d", after.Unix()))
	}
	if !before.IsZero() {
		params.Set("before", fmt.Sprintf("%d", before.Unix()))
	}
	if page > 0 {
		params.Set("page", fmt.Sprintf("%d", page))
	}
	if perPage > 0 {
		params.Set("per_page", fmt.Sprintf("%d", perPage))
	}

	var payload []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		StartDate string `json:"start_date"`
	}

	if err := c.getJSON(ctx, "/athlete/activities", params, &payload); err != nil {
		return nil, err
	}

	activities := make([]ActivitySummary, 0, len(payload))
	for _, p := range payload {
		start, err := time.Parse(time.RFC3339, p.StartDate)
		if err != nil {
			return nil, fmt.Errorf("parse start_date: %w", err)
		}
		activities = append(activities, ActivitySummary{
			ID:        p.ID,
			Name:      p.Name,
			Type:      p.Type,
			StartDate: start,
		})
	}

	return activities, nil
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
		return newAPIError(resp, req, body)
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return err
	}

	return nil
}
