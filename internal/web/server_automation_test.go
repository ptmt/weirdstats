package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"weirdstats/internal/gps"
	"weirdstats/internal/storage"
)

const automationRideRule = `{"match":"all","conditions":[{"metric":"activity_type","op":"eq","values":["Ride"]}],"action":{"type":"hide"}}`

func newAutomationTestServer(t *testing.T) (*Server, *storage.Store) {
	t.Helper()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStravaToken(context.Background(), storage.StravaToken{UserID: 202, AccessToken: "token"}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, nil, nil, nil, gps.StopOptions{}, StravaConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return server, store
}

func automationRequest(t *testing.T, server *Server, method string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/automation", strings.NewReader(form.Encode()))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	session := httptest.NewRecorder()
	if err := server.setSession(session, req, 202); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range session.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	server.Automation(rec, req)
	return rec
}

func TestAutomation_RequiresAuthentication(t *testing.T) {
	server, _ := newAutomationTestServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		server.Automation(rec, httptest.NewRequest(method, "/automation", nil))
		if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "next=%2Fautomation") {
			t.Fatalf("expected sign-in redirect for %s, got %d %s", method, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestAutomation_ShowsSavedJSON(t *testing.T) {
	server, store := newAutomationTestServer(t)
	ctx := context.Background()
	ruleID, err := store.CreateHideRule(ctx, storage.HideRule{UserID: 202, Name: "Saved rides", Condition: automationRideRule, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	legacyCondition := `</textarea><script>alert("legacy")</script>`
	if _, err := store.CreateHideRule(ctx, storage.HideRule{UserID: 202, Name: "Legacy rule", Condition: legacyCondition}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateHideRule(ctx, storage.HideRule{UserID: 303, Name: "Another user's rule", Condition: automationRideRule}); err != nil {
		t.Fatal(err)
	}
	rec := automationRequest(t, server, http.MethodGet, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, text := range []string{
		`href="/automation" class="active" aria-current="page"`,
		"View and edit JSON",
		"Save changes",
		`name="action" value="update-rule"`,
		`id="rule-json-` + strconv.FormatInt(ruleID, 10) + `"`,
		`"metric": "activity_type"`,
		"Optional: generate a rule with an LLM",
		"Legacy rule",
	} {
		if !strings.Contains(html.UnescapeString(body), text) {
			t.Fatalf("expected %q in Automation", text)
		}
	}
	if strings.Contains(body, legacyCondition) || !strings.Contains(html.UnescapeString(body), legacyCondition) {
		t.Fatal("expected legacy condition to be preserved and HTML-escaped")
	}
	if strings.Contains(body, "Another user") || strings.Contains(body, "Weirdstats facts") {
		t.Fatal("Automation should show only the current user's rules")
	}
}

func TestAutomation_UpdateRule(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			server, store := newAutomationTestServer(t)
			ctx := context.Background()
			ruleID, err := store.CreateHideRule(ctx, storage.HideRule{UserID: 202, Name: "Rides", Condition: automationRideRule, Enabled: enabled})
			if err != nil {
				t.Fatal(err)
			}
			updatedJSON := `{"conditions":[{"metric":"activity_type","op":"in","values":["Ride","Walk","Workout"]}],"action":{"type":"hide","override":{"one_in":10}}}`
			rec := automationRequest(t, server, http.MethodPost, url.Values{
				"action":    {"update-rule"},
				"rule_id":   {strconv.FormatInt(ruleID, 10)},
				"name":      {"  More activities  "},
				"condition": {updatedJSON},
			})
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/automation?msg=rule+updated" {
				t.Fatalf("unexpected response: %d %s", rec.Code, rec.Header().Get("Location"))
			}
			rows, err := store.ListHideRules(ctx, 202)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].ID != ruleID || rows[0].Name != "More activities" || rows[0].Enabled != enabled {
				t.Fatalf("expected in-place update preserving enabled status, got %+v", rows)
			}
			expectedJSON := `{"match":"all","conditions":[{"metric":"activity_type","op":"in","values":["Ride","Walk","Workout"]}],"action":{"type":"hide","override":{"one_in":10}}}`
			if rows[0].Condition != expectedJSON {
				t.Fatalf("unexpected normalized rule: %s", rows[0].Condition)
			}
		})
	}
}

func TestAutomation_RejectsInvalidUpdates(t *testing.T) {
	for _, test := range []struct {
		name      string
		userID    int64
		ruleID    string
		condition string
	}{
		{name: "malformed JSON", userID: 202, condition: "{"},
		{name: "invalid definition", userID: 202, condition: `{"conditions":[{"metric":"unknown","op":"eq","values":[1]}]}`},
		{name: "invalid id", userID: 202, ruleID: "invalid", condition: automationRideRule},
		{name: "negative id", userID: 202, ruleID: "-1", condition: automationRideRule},
		{name: "missing row", userID: 202, ruleID: "99999", condition: automationRideRule},
		{name: "another user's rule", userID: 303, condition: automationRideRule},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, store := newAutomationTestServer(t)
			ctx := context.Background()
			ruleID, err := store.CreateHideRule(ctx, storage.HideRule{UserID: test.userID, Name: "Original", Condition: automationRideRule, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			id := test.ruleID
			if id == "" {
				id = strconv.FormatInt(ruleID, 10)
			}
			rec := automationRequest(t, server, http.MethodPost, url.Values{
				"action": {"update-rule"}, "rule_id": {id}, "name": {"Changed"}, "condition": {test.condition},
			})
			if rec.Code != http.StatusFound || rec.Header().Get("Location") == "/automation?msg=rule+updated" {
				t.Fatalf("expected rejected update, got %d %s", rec.Code, rec.Header().Get("Location"))
			}
			rows, err := store.ListHideRules(ctx, test.userID)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Name != "Original" || rows[0].Condition != automationRideRule || !rows[0].Enabled {
				t.Fatalf("rejected update changed the stored rule: %+v", rows)
			}
		})
	}
}

func TestAutomation_RuleLifecycle(t *testing.T) {
	server, store := newAutomationTestServer(t)
	ctx := context.Background()
	rec := automationRequest(t, server, http.MethodPost, url.Values{
		"action": {"add-rule"}, "name": {"Rides"}, "condition": {automationRideRule}, "enabled": {"on"},
	})
	if rec.Header().Get("Location") != "/automation?msg=rule+added" {
		t.Fatalf("unexpected add response: %s", rec.Header().Get("Location"))
	}
	rows, err := store.ListHideRules(ctx, 202)
	if err != nil || len(rows) != 1 || !rows[0].Enabled {
		t.Fatalf("expected one enabled rule, got %+v, %v", rows, err)
	}
	ruleID := strconv.FormatInt(rows[0].ID, 10)
	automationRequest(t, server, http.MethodPost, url.Values{"action": {"toggle-rule"}, "rule_id": {ruleID}})
	rows, err = store.ListHideRules(ctx, 202)
	if err != nil || len(rows) != 1 || rows[0].Enabled {
		t.Fatalf("expected disabled rule, got %+v, %v", rows, err)
	}
	automationRequest(t, server, http.MethodPost, url.Values{"action": {"delete-rule"}, "rule_id": {ruleID}})
	rows, err = store.ListHideRules(ctx, 202)
	if err != nil || len(rows) != 0 {
		t.Fatalf("expected deleted rule, got %+v, %v", rows, err)
	}
}

func TestAutomation_ShowsPaceRuleTemplate(t *testing.T) {
	server, _ := newAutomationTestServer(t)
	rec := automationRequest(t, server, http.MethodGet, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	for _, text := range []string{"pace_sec_per_km", "Hide slow rides or runs", "6:00/km"} {
		if !strings.Contains(rec.Body.String(), text) {
			t.Fatalf("expected %q in automation page", text)
		}
	}
}
