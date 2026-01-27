package rules

import (
	"testing"
)

func TestValidateRule(t *testing.T) {
	reg := DefaultRegistry()
	parsed, err := ParseRuleJSON(`{"match":"all","conditions":[{"metric":"distance_m","op":"lt","values":[20000]}],"action":{"type":"hide"}}`)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	if err := ValidateRule(parsed, reg); err != nil {
		t.Fatalf("validate rule: %v", err)
	}

	invalid, err := ParseRuleJSON(`{"match":"all","conditions":[{"metric":"distance_m","op":"between","values":[100]}],"action":{"type":"hide"}}`)
	if err != nil {
		t.Fatalf("parse invalid rule: %v", err)
	}
	if err := ValidateRule(invalid, reg); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestEvaluateRule(t *testing.T) {
	reg := DefaultRegistry()
	parsed, err := ParseRuleJSON(`{"match":"all","conditions":[{"metric":"distance_m","op":"lt","values":[20000]},{"metric":"activity_type","op":"eq","values":["Ride"]}],"action":{"type":"hide","allow":{"one_in":10}}}`)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	ctx := Context{
		Activity: ActivitySource{ID: 42, Type: "Ride", DistanceM: 15000, MovingTimeS: 3600, StartUnix: 1700000000},
		Stats:    StatsSource{},
	}
	matched, hide, err := Evaluate(parsed, reg, ctx, 7)
	if err != nil {
		t.Fatalf("evaluate rule: %v", err)
	}
	if !matched {
		t.Fatalf("expected match")
	}
	allowed := allowOneIn(7, 42, 10)
	if hide == allowed {
		t.Fatalf("expected hide to be inverse of allow decision")
	}
}
