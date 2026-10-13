package web

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"weirdstats/internal/storage"
)

type AutomationEventView struct {
	ID           int64
	ActivityID   int64
	ActivityName string
	Name         string
	Status       string
	StatusLabel  string
	Source       string
	Detail       string
	Time         string
	DateTime     string
	Triggered    bool
}

func automationHistoryViews(events []storage.AutomationEvent) []AutomationEventView {
	views := make([]AutomationEventView, 0, len(events))
	labels := map[string]string{"checking": "Checking", "pending": "Running", "applied": "Applied", "failed": "Failed", "no_match": "No match", "override": "Kept visible", "disabled": "Disabled", "invalid": "Invalid rule", "unchanged": "No change", "already_hidden": "Already muted", "matched": "Matched in analysis"}
	sources := map[string]string{"automatic": "Automatic", "manual": "Manual", "analysis": "Analysis", "processing": "Processing"}
	for _, event := range events {
		views = append(views, AutomationEventView{ID: event.ID, ActivityID: event.ActivityID, ActivityName: event.ActivityName, Name: event.Name, Status: event.Status, StatusLabel: labels[event.Status], Source: sources[event.Source], Detail: event.Detail, Time: formatTimestamp(event.CreatedAt), DateTime: event.CreatedAt.Format(time.RFC3339), Triggered: event.Triggered})
	}
	return views
}

func (s *Server) loadAutomationHistory(w http.ResponseWriter, r *http.Request, userID int64, data *AutomationPageData) bool {
	activityID, beforeID := int64(0), int64(0)
	for name, target := range map[string]*int64{"activity": &activityID, "before": &beforeID} {
		if raw := r.URL.Query().Get(name); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				http.Error(w, "invalid history filter", http.StatusBadRequest)
				return false
			}
			*target = id
		}
	}
	if activityID != 0 {
		activity, err := s.store.GetActivityForUser(r.Context(), userID, activityID)
		if err != nil {
			http.NotFound(w, r)
			return false
		}
		data.HistoryActivityID, data.HistoryActivityName = activityID, activity.Name
	}
	events, err := s.store.ListAutomationEvents(r.Context(), userID, activityID, beforeID, 51)
	if err != nil {
		http.Error(w, "failed to load automation history", 500)
		return false
	}
	if len(events) > 50 {
		events = events[:50]
		query := url.Values{"before": {strconv.FormatInt(events[49].ID, 10)}}
		if activityID != 0 {
			query.Set("activity", strconv.FormatInt(activityID, 10))
		}
		data.HistoryNextURL = "/automation?" + query.Encode() + "#history"
	}
	data.History = automationHistoryViews(events)
	return true
}
