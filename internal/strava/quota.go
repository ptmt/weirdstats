package strava

import (
	"net/http"
	"sync"
	"time"

	"weirdstats/internal/storage"
)

// A factory shares this transport across athletes. Cooldowns survive restarts.
type quotaTransport struct {
	base  http.RoundTripper
	store *storage.Store
	mu    sync.Mutex
}

func (t *quotaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	until, err := t.store.APICooldown(req.Context(), "strava")
	if err != nil {
		return nil, err
	}
	if time.Now().Before(until) {
		return nil, &APIError{StatusCode: http.StatusTooManyRequests, RateLimit: RateLimitInfo{RetryAt: until}}
	}
	if storage.IsBackfill(req.Context()) {
		until, err = t.store.APICooldown(req.Context(), "strava_backfill")
		if err != nil {
			return nil, err
		}
		if time.Now().Before(until) {
			return nil, &APIError{StatusCode: 429, QuotaBucket: "strava_backfill", RateLimit: RateLimitInfo{RetryAt: until}}
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	info := parseRateLimitInfo(resp.Header)
	reserved := info
	reserve := func(limit int, cap int) int {
		if limit <= 0 {
			return limit
		}
		headroom := limit / 10
		if headroom > cap {
			headroom = cap
		}
		return limit - headroom
	}
	reserved.ReadLimitShort = reserve(info.ReadLimitShort, 10)
	reserved.ReadLimitLong = reserve(info.ReadLimitLong, 100)
	reserved.LimitShort = reserve(info.LimitShort, 10)
	reserved.LimitLong = reserve(info.LimitLong, 100)
	if reset := reserved.resetAt(time.Now(), false); !reset.IsZero() {
		if err = t.store.SetAPICooldown(req.Context(), "strava_backfill", reset); err != nil {
			resp.Body.Close()
			return nil, err
		}
	}
	until = info.resetAt(time.Now(), resp.StatusCode == http.StatusTooManyRequests)
	if !until.IsZero() {
		if err := t.store.SetAPICooldown(req.Context(), "strava", until); err != nil {
			resp.Body.Close()
			return nil, err
		}
	}
	return resp, nil
}

func (r RateLimitInfo) resetAt(now time.Time, limited bool) time.Time {
	var until time.Time
	advance := func(value time.Time) {
		if value.After(until) {
			until = value
		}
	}
	if r.RetryAfter > 0 {
		advance(now.Add(r.RetryAfter))
	}
	if r.RetryAt.After(now) {
		advance(r.RetryAt)
	}
	if (r.LimitShort > 0 && r.UsageShort >= r.LimitShort) || (r.ReadLimitShort > 0 && r.ReadUsageShort >= r.ReadLimitShort) {
		advance(now.UTC().Truncate(15 * time.Minute).Add(15*time.Minute + time.Second))
	}
	if (r.LimitLong > 0 && r.UsageLong >= r.LimitLong) || (r.ReadLimitLong > 0 && r.ReadUsageLong >= r.ReadLimitLong) {
		advance(now.UTC().Truncate(24 * time.Hour).Add(24*time.Hour + time.Second))
	}
	if limited && until.IsZero() {
		advance(now.UTC().Truncate(15 * time.Minute).Add(15*time.Minute + time.Second))
	}
	return until
}
