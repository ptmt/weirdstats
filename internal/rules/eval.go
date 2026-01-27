package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strconv"
	"strings"
)

var (
	ErrInvalidRule     = errors.New("invalid rule")
	ErrInvalidOperator = errors.New("invalid operator")
)

func ParseRuleJSON(raw string) (Rule, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var rule Rule
	if err := dec.Decode(&rule); err != nil {
		return Rule{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Rule{}, fmt.Errorf("%w: trailing data", ErrInvalidRule)
	}
	if rule.Match == "" {
		rule.Match = "all"
	}
	if rule.Action.Type == "" {
		rule.Action.Type = "hide"
	}
	return rule, nil
}

func ValidateRule(rule Rule, reg Registry) error {
	if len(rule.Conditions) == 0 {
		return fmt.Errorf("%w: at least one condition required", ErrInvalidRule)
	}
	switch rule.Match {
	case "all", "any":
	default:
		return fmt.Errorf("%w: match must be all or any", ErrInvalidRule)
	}
	if rule.Action.Type != "" && rule.Action.Type != "hide" {
		return fmt.Errorf("%w: unsupported action", ErrInvalidRule)
	}
	if rule.Action.Allow != nil && rule.Action.Allow.OneIn > 0 && rule.Action.Allow.OneIn < 2 {
		return fmt.Errorf("%w: allow.one_in must be >= 2", ErrInvalidRule)
	}
	ops := DefaultOperators()
	for _, cond := range rule.Conditions {
		metric, ok := reg[cond.Metric]
		if !ok {
			return fmt.Errorf("%w: unknown metric %s", ErrInvalidRule, cond.Metric)
		}
		operator := operatorSpec(ops, metric.Type, cond.Op)
		if operator == nil {
			return fmt.Errorf("%w: invalid operator %s", ErrInvalidOperator, cond.Op)
		}
		if err := validateValues(metric.Type, *operator, cond.Values); err != nil {
			return err
		}
	}
	return nil
}

func Evaluate(rule Rule, reg Registry, ctx Context, ruleID int64) (bool, bool, error) {
	matchAll := rule.Match != "any"
	matched := matchAll
	ops := DefaultOperators()
	for _, cond := range rule.Conditions {
		metric, ok := reg[cond.Metric]
		if !ok {
			return false, false, fmt.Errorf("unknown metric %s", cond.Metric)
		}
		operator := operatorSpec(ops, metric.Type, cond.Op)
		if operator == nil {
			return false, false, fmt.Errorf("invalid operator %s", cond.Op)
		}
		if err := validateValues(metric.Type, *operator, cond.Values); err != nil {
			return false, false, err
		}
		value, err := metric.Resolve(ctx)
		if err != nil {
			return false, false, err
		}
		conditionMatched, err := evalCondition(metric.Type, cond.Op, value, cond.Values)
		if err != nil {
			return false, false, err
		}
		if matchAll {
			if !conditionMatched {
				matched = false
				break
			}
		} else if conditionMatched {
			matched = true
			break
		}
	}
	if !matched {
		return false, false, nil
	}
	if rule.Action.Type != "" && rule.Action.Type != "hide" {
		return true, false, fmt.Errorf("unsupported action %s", rule.Action.Type)
	}
	if rule.Action.Allow != nil && rule.Action.Allow.OneIn >= 2 {
		allowed := allowOneIn(ruleID, ctx.Activity.ID, rule.Action.Allow.OneIn)
		return true, !allowed, nil
	}
	return true, true, nil
}

func Describe(rule Rule, reg Registry) string {
	ops := DefaultOperators()
	parts := make([]string, 0, len(rule.Conditions))
	joiner := " AND "
	if rule.Match == "any" {
		joiner = " OR "
	}
	for _, cond := range rule.Conditions {
		metric, ok := reg[cond.Metric]
		if !ok {
			parts = append(parts, cond.Metric)
			continue
		}
		operator := operatorSpec(ops, metric.Type, cond.Op)
		label := cond.Op
		if operator != nil {
			label = operator.Label
		}
		valueText := formatValues(metric.Type, metric.Unit, cond.Values)
		parts = append(parts, fmt.Sprintf("%s %s %s", metric.Label, label, valueText))
	}
	description := strings.Join(parts, joiner)
	if rule.Action.Allow != nil && rule.Action.Allow.OneIn >= 2 {
		description += fmt.Sprintf(" · allow 1 in %d", rule.Action.Allow.OneIn)
	}
	return description
}

func operatorSpec(ops map[ValueType][]OperatorSpec, valueType ValueType, op string) *OperatorSpec {
	for _, candidate := range ops[valueType] {
		if candidate.ID == op {
			copy := candidate
			return &copy
		}
	}
	return nil
}

func validateValues(valueType ValueType, operator OperatorSpec, values []any) error {
	count := len(values)
	switch operator.ValueCount {
	case 1:
		if count != 1 {
			return fmt.Errorf("%w: operator %s expects one value", ErrInvalidRule, operator.ID)
		}
	case 2:
		if count != 2 {
			return fmt.Errorf("%w: operator %s expects two values", ErrInvalidRule, operator.ID)
		}
	case -1:
		if count < 1 {
			return fmt.Errorf("%w: operator %s expects at least one value", ErrInvalidRule, operator.ID)
		}
	}
	if valueType == ValueNumber {
		for _, v := range values {
			if _, ok := toFloat(v); !ok {
				return fmt.Errorf("%w: numeric value expected", ErrInvalidRule)
			}
		}
		return nil
	}
	if valueType == ValueEnum {
		for _, v := range values {
			if _, ok := toString(v); !ok {
				return fmt.Errorf("%w: string value expected", ErrInvalidRule)
			}
		}
		return nil
	}
	return fmt.Errorf("%w: unsupported metric type", ErrInvalidRule)
}

func evalCondition(valueType ValueType, op string, metricValue Value, rawValues []any) (bool, error) {
	switch valueType {
	case ValueNumber:
		values, err := parseNumberValues(rawValues)
		if err != nil {
			return false, err
		}
		return evalNumber(op, metricValue.Num, values)
	case ValueEnum:
		values, err := parseStringValues(rawValues)
		if err != nil {
			return false, err
		}
		return evalEnum(op, metricValue.Str, values)
	default:
		return false, fmt.Errorf("unsupported value type")
	}
}

func parseNumberValues(values []any) ([]float64, error) {
	out := make([]float64, 0, len(values))
	for _, v := range values {
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("invalid number")
		}
		out = append(out, f)
	}
	return out, nil
}

func parseStringValues(values []any) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, v := range values {
		s, ok := toString(v)
		if !ok {
			return nil, fmt.Errorf("invalid string")
		}
		out = append(out, s)
	}
	return out, nil
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func toString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case fmt.Stringer:
		return v.String(), true
	default:
		return "", false
	}
}

func evalNumber(op string, metric float64, values []float64) (bool, error) {
	switch op {
	case "eq":
		return metric == values[0], nil
	case "neq":
		return metric != values[0], nil
	case "lt":
		return metric < values[0], nil
	case "lte":
		return metric <= values[0], nil
	case "gt":
		return metric > values[0], nil
	case "gte":
		return metric >= values[0], nil
	case "between":
		min := values[0]
		max := values[1]
		if min > max {
			min, max = max, min
		}
		return metric >= min && metric <= max, nil
	default:
		return false, ErrInvalidOperator
	}
}

func evalEnum(op string, metric string, values []string) (bool, error) {
	metricNorm := strings.ToLower(metric)
	switch op {
	case "eq":
		return strings.ToLower(values[0]) == metricNorm, nil
	case "neq":
		return strings.ToLower(values[0]) != metricNorm, nil
	case "in":
		for _, v := range values {
			if strings.ToLower(v) == metricNorm {
				return true, nil
			}
		}
		return false, nil
	case "not_in":
		for _, v := range values {
			if strings.ToLower(v) == metricNorm {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, ErrInvalidOperator
	}
}

func formatValues(valueType ValueType, unit string, values []any) string {
	switch valueType {
	case ValueNumber:
		nums, err := parseNumberValues(values)
		if err != nil {
			return "?"
		}
		parts := make([]string, 0, len(nums))
		for _, n := range nums {
			parts = append(parts, formatNumber(n, unit))
		}
		return strings.Join(parts, " and ")
	case ValueEnum:
		vals, err := parseStringValues(values)
		if err != nil {
			return "?"
		}
		return strings.Join(vals, ", ")
	default:
		return "?"
	}
}

func formatNumber(value float64, unit string) string {
	if unit == "" {
		return trimFloat(value)
	}
	return fmt.Sprintf("%s %s", trimFloat(value), unit)
}

func trimFloat(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func allowOneIn(ruleID int64, activityID int64, n int) bool {
	if n <= 1 {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%d:%d", ruleID, activityID)))
	return int(h.Sum64()%uint64(n)) == 0
}
