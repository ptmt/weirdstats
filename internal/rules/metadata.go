package rules

import "sort"

type MetricMeta struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Unit        string   `json:"unit"`
	Example     string   `json:"example"`
	Type        ValueType `json:"type"`
	Enum        []string `json:"enum,omitempty"`
}

type Metadata struct {
	Metrics   []MetricMeta                `json:"metrics"`
	Operators map[ValueType][]OperatorSpec `json:"operators"`
}

func BuildMetadata(reg Registry, ops map[ValueType][]OperatorSpec) Metadata {
	metrics := make([]MetricMeta, 0, len(reg))
	for _, metric := range reg {
		metrics = append(metrics, MetricMeta{
			ID:          metric.ID,
			Label:       metric.Label,
			Description: metric.Description,
			Unit:        metric.Unit,
			Example:     metric.Example,
			Type:        metric.Type,
			Enum:        append([]string(nil), metric.Enum...),
		})
	}
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].ID < metrics[j].ID
	})
	return Metadata{Metrics: metrics, Operators: ops}
}
