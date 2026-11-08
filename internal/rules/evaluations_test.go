package rules

import (
	"strings"
	"testing"
)

func TestEvaluateDefinitionsReportsEveryRule(t *testing.T) {
	virtualRide := `{"conditions":[{"metric":"activity_type","op":"in","values":["VirtualRide","Workout"]}]}`
	definitions := []Definition{
		{ID: 1, Name: "Workout only", Enabled: true, Condition: `{"conditions":[{"metric":"activity_type","op":"eq","values":["Workout"]}]}`},
		{ID: 2, Name: "Virtual rides", Enabled: true, Condition: virtualRide},
		{ID: 3, Name: "Broken", Enabled: true, Condition: "{"},
		{ID: 4, Name: "Disabled", Condition: virtualRide},
		{ID: 5, Name: "Another matching rule", Enabled: true, Condition: virtualRide},
	}
	hide, evaluations := EvaluateDefinitions(definitions, DefaultRegistry(), Context{Activity: ActivitySource{Type: "VirtualRide", DistanceM: 18100, MovingTimeS: 2697}})
	if !hide || len(evaluations) != len(definitions) {
		t.Fatalf("missing evaluations: %v %+v", hide, evaluations)
	}
	for i, status := range []string{"no_match", "matched", "invalid", "disabled", "matched"} {
		if evaluations[i].Status != status {
			t.Fatalf("evaluation %d: %+v", i, evaluations[i])
		}
	}
	if !strings.Contains(evaluations[0].Detail, "VirtualRide") || !strings.Contains(evaluations[0].Detail, "Workout") {
		t.Fatal("rule mismatch omitted actual activity type or expected condition")
	}
}
