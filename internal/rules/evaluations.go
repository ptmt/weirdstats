package rules

import "fmt"

type Definition struct {
	ID        int64
	Name      string
	Condition string
	Enabled   bool
}

type Evaluation struct {
	RuleID     int64
	Name       string
	Status     string
	Detail     string
	Matched    bool
	ShouldHide bool
}

// EvaluateDefinitions checks every rule, preserving the any-hide-wins decision
// while retaining the reasons for matches, overrides and skipped rules.
func EvaluateDefinitions(definitions []Definition, registry Registry, ctx Context) (bool, []Evaluation) {
	hide := false
	evaluations := make([]Evaluation, 0, len(definitions))
	for _, definition := range definitions {
		evaluation := Evaluation{RuleID: definition.ID, Name: definition.Name, Status: "disabled", Detail: "This rule is disabled."}
		if definition.Enabled {
			rule, err := ParseRuleJSON(definition.Condition)
			if err == nil {
				err = ValidateRule(rule, registry)
			}
			if err != nil {
				evaluation.Status, evaluation.Detail = "invalid", "This rule is invalid. Edit its JSON in Automation."
			} else {
				evaluation.Matched, evaluation.ShouldHide, err = Evaluate(rule, registry, ctx, definition.ID)
				evaluation.Detail = Describe(rule, registry)
				switch {
				case err != nil:
					evaluation.Status, evaluation.Detail = "failed", "This rule could not be evaluated for this activity."
				case !evaluation.Matched:
					evaluation.Status = "no_match"
					evaluation.Detail += fmt.Sprintf(". Conditions did not match; this activity is %s, %.0f m, %d seconds moving.", ctx.Activity.Type, ctx.Activity.DistanceM, ctx.Activity.MovingTimeS)
				case !evaluation.ShouldHide:
					evaluation.Status = "override"
					evaluation.Detail += ". The visibility override kept this activity visible for this rule."
				default:
					evaluation.Status = "matched"
					hide = true
				}
			}
		}
		evaluations = append(evaluations, evaluation)
	}
	return hide, evaluations
}
