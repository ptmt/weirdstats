package strava

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

const RequestedScopes = "read,activity:read,activity:read_all,activity:write"

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

func NormalizeScopes(raw string) string {
	seen := map[string]bool{}
	for _, scope := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		seen[scope] = true
	}
	var result []string
	for scope := range seen {
		result = append(result, scope)
	}
	sort.Strings(result)
	return strings.Join(result, ",")
}

func HasScope(scopes, required string) bool {
	for _, scope := range strings.Split(NormalizeScopes(scopes), ",") {
		if scope == required {
			return true
		}
	}
	return false
}

func CanReadActivities(scopes string) bool {
	return HasScope(scopes, "activity:read") || HasScope(scopes, "activity:read_all")
}

type PermissionError struct{ Scope string }

func (e *PermissionError) Error() string { return "Strava permission required: " + e.Scope }

// SafeError excludes provider bodies and query URLs, which may include locations.
func SafeError(err error) (code, message string) {
	var permission *PermissionError
	if errors.As(err, &permission) {
		if permission.Scope == "activity:write" {
			return "write_permission_required", "Imported data is available. Reconnect Strava to allow publishing updates."
		}
		return "read_permission_required", "Reconnect Strava and allow access to activities."
	}
	var api *APIError
	if errors.As(err, &api) {
		if api.StatusCode == http.StatusBadRequest && api.Path == "/oauth/token" {
			return "authorization_required", "Reconnect Strava to restore the required access."
		}
		switch api.StatusCode {
		case 400, 422:
			return "request_rejected", "Strava rejected this request. Check your settings or contact support with the job number."
		case 429:
			return "rate_limited", "Waiting for Strava's allowance to reset. Your progress is saved."
		case 401, 403:
			return "authorization_required", "Reconnect Strava to restore the required access."
		case 404:
			return "activity_unavailable", "This activity or its data is no longer available from Strava."
		default:
			return "strava_unavailable", "Strava is temporarily unavailable. We will retry automatically."
		}
	}
	return "processing_error", "Processing could not finish. Retry this activity or contact support with the job number."
}
