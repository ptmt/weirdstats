package strava

import (
	"errors"
	"net/http"
	"time"
)

func IsRateLimited(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests
	}
	return false
}

func RateLimitInfoFromError(err error) (RateLimitInfo, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.RateLimit.HasData() {
			return apiErr.RateLimit, true
		}
	}
	return RateLimitInfo{}, false
}

func RateLimitBackoff(err error) (time.Duration, bool) {
	info, ok := RateLimitInfoFromError(err)
	if !ok {
		return 0, false
	}
	if info.RetryAfter > 0 {
		return info.RetryAfter, true
	}
	if !info.RetryAt.IsZero() {
		wait := time.Until(info.RetryAt)
		if wait > 0 {
			return wait, true
		}
	}
	return 0, false
}
