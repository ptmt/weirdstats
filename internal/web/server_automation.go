package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"weirdstats/internal/rules"
	"weirdstats/internal/storage"
)

func (s *Server) Automation(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/activities/automation" {
		http.NotFound(w, r)
		return
	}
	userID, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		s.handleAutomationPost(w, r, userID)
		return
	}

	registry := rules.DefaultRegistry()
	rows, err := s.store.ListHideRules(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to load rules", http.StatusInternalServerError)
		return
	}
	var viewRules []AutomationRule
	for _, row := range rows {
		description := row.Condition
		condition := row.Condition
		isLegacy := false
		if parsed, err := rules.ParseRuleJSON(row.Condition); err == nil {
			description = rules.Describe(parsed, registry)
			var formatted bytes.Buffer
			if err := json.Indent(&formatted, []byte(row.Condition), "", "  "); err == nil {
				condition = formatted.String()
			}
		} else {
			isLegacy = true
		}
		viewRules = append(viewRules, AutomationRule{
			ID:          row.ID,
			Name:        row.Name,
			Description: description,
			Condition:   condition,
			Enabled:     row.Enabled,
			IsLegacy:    isLegacy,
		})
	}
	metaJSON, err := json.Marshal(rules.BuildMetadata(registry, rules.DefaultOperators()))
	if err != nil {
		http.Error(w, "failed to load rule metadata", http.StatusInternalServerError)
		return
	}
	data := AutomationPageData{
		PageData: PageData{
			Title:      "Automation",
			Page:       "automation",
			Message:    r.URL.Query().Get("msg"),
			FooterText: "Automations apply when Weirdstats processes activities.",
			Strava:     s.getStravaInfo(r.Context(), userID),
			UserCount:  s.userCount(r.Context()),
		},
		Rules:         viewRules,
		RulesMetaJSON: template.JS(string(metaJSON)),
	}
	if err := s.templates["automation"].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *Server) handleAutomationPost(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/activities/automation?msg=invalid+form", http.StatusFound)
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "add-rule", "update-rule":
		var ruleID int64
		if action == "update-rule" {
			var err error
			ruleID, err = strconv.ParseInt(r.FormValue("rule_id"), 10, 64)
			if err != nil || ruleID <= 0 {
				http.Redirect(w, r, "/activities/automation?msg=invalid+rule", http.StatusFound)
				return
			}
		}
		name := strings.TrimSpace(r.FormValue("name"))
		condition := strings.TrimSpace(r.FormValue("condition"))
		enabled := r.FormValue("enabled") == "on"
		if name == "" || condition == "" {
			http.Redirect(w, r, "/activities/automation?msg=missing+rule+fields", http.StatusFound)
			return
		}
		parsedRule, err := rules.ParseRuleJSON(condition)
		if err != nil {
			detail := compactErrMessage(err)
			log.Printf("automation %s parse failed: name=%q err=%v json=%q", action, name, err, compactForLog(condition, 500))
			http.Redirect(w, r, "/activities/automation?msg="+url.QueryEscape("invalid rule json: "+detail), http.StatusFound)
			return
		}
		if err := rules.ValidateRule(parsedRule, rules.DefaultRegistry()); err != nil {
			detail := compactErrMessage(err)
			log.Printf("automation %s validation failed: name=%q err=%v json=%q", action, name, err, compactForLog(condition, 500))
			http.Redirect(w, r, "/activities/automation?msg="+url.QueryEscape("invalid rule definition: "+detail), http.StatusFound)
			return
		}
		normalized, err := json.Marshal(parsedRule)
		if err != nil {
			log.Printf("automation %s normalize failed: name=%q err=%v", action, name, err)
			http.Redirect(w, r, "/activities/automation?msg=rule+save+failed", http.StatusFound)
			return
		}
		condition = string(normalized)
		if action == "update-rule" {
			if err := s.store.UpdateHideRuleForUser(r.Context(), userID, ruleID, name, condition); err != nil {
				log.Printf("automation update-rule store failed: rule_id=%d err=%v", ruleID, err)
				http.Redirect(w, r, "/activities/automation?msg=rule+update+failed", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/activities/automation?msg=rule+updated", http.StatusFound)
			return
		}
		if _, err := s.store.CreateHideRule(r.Context(), storage.HideRule{
			UserID:    userID,
			Name:      name,
			Condition: condition,
			Enabled:   enabled,
		}); err != nil {
			log.Printf("automation add-rule store failed: name=%q enabled=%t err=%v", name, enabled, err)
			http.Redirect(w, r, "/activities/automation?msg=rule+save+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/activities/automation?msg=rule+added", http.StatusFound)
	case "toggle-rule":
		idValue := r.FormValue("rule_id")
		enabled := r.FormValue("enabled") == "on"
		ruleID, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || ruleID == 0 {
			http.Redirect(w, r, "/activities/automation?msg=invalid+rule", http.StatusFound)
			return
		}
		if err := s.store.UpdateHideRuleEnabledForUser(r.Context(), userID, ruleID, enabled); err != nil {
			http.Redirect(w, r, "/activities/automation?msg=rule+update+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/activities/automation?msg=rule+updated", http.StatusFound)
	case "delete-rule":
		idValue := r.FormValue("rule_id")
		ruleID, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || ruleID == 0 {
			http.Redirect(w, r, "/activities/automation?msg=invalid+rule", http.StatusFound)
			return
		}
		if err := s.store.DeleteHideRuleForUser(r.Context(), userID, ruleID); err != nil {
			http.Redirect(w, r, "/activities/automation?msg=rule+delete+failed", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/activities/automation?msg=rule+deleted", http.StatusFound)
	default:
		http.Redirect(w, r, "/activities/automation?msg=unknown+action", http.StatusFound)
	}
}
