package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"weirdstats/internal/storage"
)

func (s *Server) forUser(ctx context.Context, userID int64) (*Server, error) {
	prefs, err := s.store.ProcessingPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	copy := *s
	if !prefs.ExternalMaps {
		copy.overpass = nil
		copy.mapAPI = nil
	}
	return &copy, nil
}

func (s *Server) Enrich(ctx context.Context, id int64) error {
	var guardErr error
	ctx, guardErr = s.store.GuardActivityContext(ctx, id)
	if guardErr != nil {
		return guardErr
	}
	activity, err := s.store.GetActivity(ctx, id)
	if err != nil {
		return err
	}
	s, err = s.forUser(ctx, activity.UserID)
	if err != nil {
		return err
	}
	points, err := s.store.LoadActivityPoints(ctx, id)
	if err != nil {
		return err
	}
	stops, err := s.store.LoadActivityStops(ctx, id)
	if err != nil {
		return err
	}
	snapshot, err := s.store.GetActivityStats(ctx, id)
	if err != nil {
		return err
	}
	return s.updateActivityDetectedFactsCache(ctx, activity, snapshot, points, stops, rideSegmentFact{}, nil, heartRateChangeFact{}, coffeeStopFact{}, routeHighlightFact{}, roadCrossingFact{})
}

type SyncView struct {
	storage.SyncStatus
	Label   string          `json:"label"`
	State   string          `json:"state"`
	Message string          `json:"message"`
	Errors  []SyncErrorView `json:"errors"`
}

type SyncErrorView struct {
	JobID   int64  `json:"job_id"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func (s *Server) syncView(ctx context.Context, userID int64) (SyncView, error) {
	status, err := s.store.UserSyncStatus(ctx, userID)
	if err != nil {
		return SyncView{}, err
	}
	view := SyncView{SyncStatus: status, Message: "Activity processing is up to date.", Label: "Up to date", State: "complete", Errors: []SyncErrorView{}}
	if status.Pending > 0 || status.Discovering > 0 {
		view.Message = "Importing activities. Your progress is saved."
		view.Label, view.State = "Finding activities", "working"
		if status.Pending > 0 {
			view.Label = fmt.Sprintf("Processing · %d left", status.Pending)
		}
	}
	if status.Publishing > 0 {
		view.Message = "Activities are available. Publishing requested updates to Strava."
		view.Label, view.State = "Publishing updates", "working"
	}
	if status.Enriching > 0 {
		view.Message = "Activities are available. Adding map details."
		view.Label, view.State = "Adding map details", "working"
	}
	until, err := s.store.APICooldown(ctx, "strava")
	if err != nil {
		return SyncView{}, err
	}
	if status.Pending > 0 || status.Discovering > 0 {
		backfillUntil, err := s.store.APICooldown(ctx, "strava_backfill")
		if err != nil {
			return SyncView{}, err
		}
		if backfillUntil.After(until) {
			until = backfillUntil
		}
	}
	if time.Now().Before(until) && (status.Pending > 0 || status.Discovering > 0 || status.Publishing > 0) {
		view.Message = "Waiting for Strava's allowance to reset. Your progress is saved."
		view.NextRunAt = until.Unix()
		view.Label, view.State = "Waiting for Strava", "waiting"
	}
	if status.Failed > 0 {
		view.Message = "Some activities need attention. Completed analysis is available."
		view.Label, view.State = fmt.Sprintf("%d need attention", status.Failed), "attention"
	}
	if status.Blocked > 0 {
		view.Message = "Reconnect Strava to restore the required permissions. Completed analysis is available."
		view.Label, view.State = "Reconnect Strava", "attention"
	}
	groups, err := s.store.UserJobErrorGroups(ctx, userID)
	if err != nil {
		return SyncView{}, err
	}
	for _, group := range groups {
		view.Errors = append(view.Errors, SyncErrorView{JobID: group.JobID, Stage: group.Type, Message: syncErrorMessage(group.ErrorCode), Count: group.Count})
	}
	return view, nil
}

// Only known classifications reach the UI; historical provider text may contain
// coordinates, request URLs or credentials and must remain out of the response.
func syncErrorMessage(code string) string {
	switch code {
	case "":
		return "An earlier attempt failed. Retry to check it again."
	case "authorization_required", "read_permission_required":
		return "Reconnect Strava to restore activity access."
	case "write_permission_required":
		return "Analysis is available. Reconnect Strava to allow publishing updates."
	case "activity_unavailable":
		return "This activity or its data is no longer available from Strava."
	case "request_rejected":
		return "Strava rejected the request. Check your settings or contact support."
	case "rate_limited":
		return "Waiting for Strava's allowance to reset. Your progress is saved."
	case "strava_unavailable":
		return "Strava could not complete the request. Retry to try again."
	case "map_unavailable":
		return "Analysis is available. Map details could not be completed."
	default:
		return "Processing could not finish. Retry or contact support with the reference number."
	}
}

func (s *Server) SyncStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireAPIUserID(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		// Cookie clients must be same-origin; native clients authenticate by bearer.
		if _, bearer := s.currentBearerUserID(r.Context(), r); !bearer && !sameOriginRequest(r) {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		if err := s.store.RetryUserJobs(r.Context(), userID, false); err != nil {
			http.Error(w, "failed to retry activities", 500)
			return
		}
	} else if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	view, err := s.syncView(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to load import status", 500)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}
