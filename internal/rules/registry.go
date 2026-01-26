package rules

import "time"

func DefaultRegistry() Registry {
	return Registry{
		"distance_m": {
			ID:          "distance_m",
			Label:       "Distance",
			Description: "Total distance in meters",
			Unit:        "m",
			Example:     "20000",
			Type:        ValueNumber,
			Resolve: func(ctx Context) (Value, error) {
				return Value{Type: ValueNumber, Num: ctx.Activity.DistanceM}, nil
			},
		},
		"moving_time_s": {
			ID:          "moving_time_s",
			Label:       "Moving time",
			Description: "Moving time in seconds",
			Unit:        "s",
			Example:     "3600",
			Type:        ValueNumber,
			Resolve: func(ctx Context) (Value, error) {
				return Value{Type: ValueNumber, Num: float64(ctx.Activity.MovingTimeS)}, nil
			},
		},
		"activity_type": {
			ID:          "activity_type",
			Label:       "Activity type",
			Description: "Strava activity type",
			Unit:        "",
			Example:     "Ride",
			Type:        ValueEnum,
			Enum: []string{
				"Ride",
				"Run",
				"Walk",
				"Hike",
				"Swim",
				"Workout",
				"VirtualRide",
				"EBikeRide",
				"GravelRide",
				"TrailRun",
				"Rowing",
				"NordicSki",
			},
			Resolve: func(ctx Context) (Value, error) {
				return Value{Type: ValueEnum, Str: ctx.Activity.Type}, nil
			},
		},
		"start_hour": {
			ID:          "start_hour",
			Label:       "Start hour",
			Description: "Hour of day activity started (0-23)",
			Unit:        "h",
			Example:     "22",
			Type:        ValueNumber,
			Resolve: func(ctx Context) (Value, error) {
				if ctx.Activity.StartUnix == 0 {
					return Value{Type: ValueNumber, Num: 0}, nil
				}
				return Value{Type: ValueNumber, Num: float64(time.Unix(ctx.Activity.StartUnix, 0).Hour())}, nil
			},
		},
		"stop_count": {
			ID:          "stop_count",
			Label:       "Stop count",
			Description: "Number of detected stops",
			Unit:        "",
			Example:     "5",
			Type:        ValueNumber,
			Resolve: func(ctx Context) (Value, error) {
				return Value{Type: ValueNumber, Num: float64(ctx.Stats.StopCount)}, nil
			},
		},
		"stop_total_seconds": {
			ID:          "stop_total_seconds",
			Label:       "Stop total time",
			Description: "Total stop time in seconds",
			Unit:        "s",
			Example:     "600",
			Type:        ValueNumber,
			Resolve: func(ctx Context) (Value, error) {
				return Value{Type: ValueNumber, Num: float64(ctx.Stats.StopTotalSeconds)}, nil
			},
		},
		"traffic_light_stop_count": {
			ID:          "traffic_light_stop_count",
			Label:       "Traffic light stops",
			Description: "Stops near traffic lights",
			Unit:        "",
			Example:     "3",
			Type:        ValueNumber,
			Resolve: func(ctx Context) (Value, error) {
				return Value{Type: ValueNumber, Num: float64(ctx.Stats.TrafficLightStopCount)}, nil
			},
		},
	}
}

func DefaultOperators() map[ValueType][]OperatorSpec {
	return map[ValueType][]OperatorSpec{
		ValueNumber: {
			{ID: "eq", Label: "=", ValueCount: 1, ValueMode: "single"},
			{ID: "neq", Label: "!=", ValueCount: 1, ValueMode: "single"},
			{ID: "lt", Label: "<", ValueCount: 1, ValueMode: "single"},
			{ID: "lte", Label: "<=", ValueCount: 1, ValueMode: "single"},
			{ID: "gt", Label: ">", ValueCount: 1, ValueMode: "single"},
			{ID: "gte", Label: ">=", ValueCount: 1, ValueMode: "single"},
			{ID: "between", Label: "between", ValueCount: 2, ValueMode: "range"},
		},
		ValueEnum: {
			{ID: "eq", Label: "is", ValueCount: 1, ValueMode: "single"},
			{ID: "neq", Label: "is not", ValueCount: 1, ValueMode: "single"},
			{ID: "in", Label: "in", ValueCount: -1, ValueMode: "list"},
			{ID: "not_in", Label: "not in", ValueCount: -1, ValueMode: "list"},
		},
	}
}
