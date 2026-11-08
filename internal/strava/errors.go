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
	return time.Until(info.resetAt(time.Now(), true)), true
}
