package rules

type ValueType string

const (
	ValueNumber ValueType = "number"
	ValueEnum   ValueType = "enum"
)

type Value struct {
	Type ValueType
	Num  float64
	Str  string
}

type Context struct {
	Activity ActivitySource
	Stats    StatsSource
}

type ActivitySource struct {
	ID          int64
	Type        string
	Name        string
	StartUnix   int64
	DistanceM   float64
	MovingTimeS int
}

type StatsSource struct {
	StopCount             int
	StopTotalSeconds      int
	TrafficLightStopCount int
}

type Metric struct {
	ID          string
	Label       string
	Description string
	Unit        string
	Example     string
	Type        ValueType
	Enum        []string
	Resolve     func(ctx Context) (Value, error)
}

type Registry map[string]Metric

type Rule struct {
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
	Action     Action      `json:"action"`
}

type Condition struct {
	Metric string `json:"metric"`
	Op     string `json:"op"`
	Values []any  `json:"values"`
}

type Action struct {
	Type     string    `json:"type"`
	Override *Override `json:"override,omitempty"`
	Allow    *Allow    `json:"allow,omitempty"`
}

type Override struct {
	OneIn int `json:"one_in,omitempty"`
}

type Allow struct {
	OneIn int `json:"one_in,omitempty"`
}

type OperatorSpec struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	ValueCount int    `json:"value_count"`
	ValueMode  string `json:"value_mode"`
}
