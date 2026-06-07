package web

import (
	"context"

	"weirdstats/internal/stats"
	"weirdstats/internal/storage"
)

const (
	weirdStatsFactStopSummary       = "stop_summary"
	weirdStatsFactTrafficLightStops = "traffic_light_stops"
	weirdStatsFactLongestSegment    = "longest_segment"
	weirdStatsFactCoffeeStop        = "coffee_stop"
	weirdStatsFactRouteHighlights   = "route_highlights"
	weirdStatsFactRoadCrossings     = "road_crossings"
	weirdStatsFactAcceleration030   = "acceleration_0_30"
	weirdStatsFactAcceleration040   = "acceleration_0_40"
	weirdStatsFactDeceleration400   = "deceleration_40_0"
	weirdStatsFactDeceleration300   = "deceleration_30_0"
)

type SettingsFact struct {
	ID                    string
	Label                 string
	Description           string
	RemarkableDescription string
	Enabled               bool
	AutoPostEveryRun      bool
	RideOnly              bool
}

type weirdStatsFactDefinition struct {
	ID                      string
	Label                   string
	Description             string
	RemarkableDescription   string
	DefaultEnabled          bool
	DefaultAutoPostEveryRun bool
	RideOnly                bool
}

type weirdStatsFactSetting struct {
	Enabled          bool
	AutoPostEveryRun bool
}

type weirdStatsFactSettings map[string]weirdStatsFactSetting

const (
	remarkableFirstOrBest = "Posts when this is the first value seen, a new all-time best, or a new yearly best."
	remarkableNewPlace    = "Posts when at least one detected place has not appeared in your prior activities."
)

var weirdStatsFactDefinitions = []weirdStatsFactDefinition{
	{
		ID:                      weirdStatsFactStopSummary,
		Label:                   "Stop summary",
		Description:             "Detect stop count and total stopped time.",
		RemarkableDescription:   "Posts when stop count or total stopped time is the first value seen, a new all-time best, or a new yearly best.",
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
	},
	{
		ID:                      weirdStatsFactTrafficLightStops,
		Label:                   "Traffic-light stops",
		Description:             "Detect how many stops happened near traffic lights.",
		RemarkableDescription:   remarkableFirstOrBest,
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
	},
	{
		ID:                      weirdStatsFactLongestSegment,
		Label:                   "Longest segment",
		Description:             "Detect the longest moving segment with distance, speed, and power when available.",
		RemarkableDescription:   "Posts when segment distance is the first value seen, a new all-time best, or a new yearly best.",
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
		RideOnly:                true,
	},
	{
		ID:                      weirdStatsFactCoffeeStop,
		Label:                   "Coffee stop",
		Description:             "Detect the best cafe or food stop name.",
		RemarkableDescription:   "Posts when the cafe or food stop has not appeared in your prior activities.",
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
		RideOnly:                true,
	},
	{
		ID:                      weirdStatsFactRouteHighlights,
		Label:                   "Route highlights",
		Description:             "Detect notable landmarks near the route.",
		RemarkableDescription:   remarkableNewPlace,
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
		RideOnly:                true,
	},
	{
		ID:                      weirdStatsFactRoadCrossings,
		Label:                   "Road crossings",
		Description:             "Detect road crossings after stops, including road names when available.",
		RemarkableDescription:   remarkableFirstOrBest,
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
	},
	{
		ID:                      weirdStatsFactAcceleration030,
		Label:                   "0 to 30 km/h",
		Description:             "Detect the fastest acceleration from standstill to 30 km/h.",
		RemarkableDescription:   "Posts when this time is the first value seen, a new all-time best, or a new yearly best.",
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
		RideOnly:                true,
	},
	{
		ID:                      weirdStatsFactAcceleration040,
		Label:                   "0 to 40 km/h",
		Description:             "Detect the fastest acceleration from standstill to 40 km/h.",
		RemarkableDescription:   "Posts when this time is the first value seen, a new all-time best, or a new yearly best.",
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
		RideOnly:                true,
	},
	{
		ID:                      weirdStatsFactDeceleration400,
		Label:                   "40 to 0 km/h",
		Description:             "Detect the fastest deceleration from 40 km/h to standstill.",
		RemarkableDescription:   "Posts when this time is the first value seen, a new all-time best, or a new yearly best.",
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
		RideOnly:                true,
	},
	{
		ID:                      weirdStatsFactDeceleration300,
		Label:                   "30 to 0 km/h",
		Description:             "Detect the fastest deceleration from 30 km/h to standstill.",
		RemarkableDescription:   "Posts when this time is the first value seen, a new all-time best, or a new yearly best.",
		DefaultEnabled:          true,
		DefaultAutoPostEveryRun: true,
		RideOnly:                true,
	},
}

var weirdStatsFactDefinitionsByID = func() map[string]weirdStatsFactDefinition {
	items := make(map[string]weirdStatsFactDefinition, len(weirdStatsFactDefinitions))
	for _, item := range weirdStatsFactDefinitions {
		items[item.ID] = item
	}
	return items
}()

func (s *Server) loadWeirdStatsFactSettings(ctx context.Context, userID int64) (weirdStatsFactSettings, error) {
	settings := defaultWeirdStatsFactSettings()
	if s.store == nil {
		return settings, nil
	}
	prefs, err := s.store.ListUserFactPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, pref := range prefs {
		if _, ok := weirdStatsFactDefinitionsByID[pref.FactID]; !ok {
			continue
		}
		settings[pref.FactID] = weirdStatsFactSetting{
			Enabled:          pref.Enabled,
			AutoPostEveryRun: pref.PostToStrava,
		}
	}
	return settings, nil
}

func buildSettingsFacts(settings weirdStatsFactSettings) []SettingsFact {
	facts := make([]SettingsFact, 0, len(weirdStatsFactDefinitions))
	for _, def := range weirdStatsFactDefinitions {
		facts = append(facts, SettingsFact{
			ID:                    def.ID,
			Label:                 def.Label,
			Description:           def.Description,
			RemarkableDescription: def.RemarkableDescription,
			Enabled:               weirdStatsFactEnabled(settings, def.ID),
			AutoPostEveryRun:      weirdStatsFactAutoPostEveryRun(settings, def.ID),
			RideOnly:              def.RideOnly,
		})
	}
	return facts
}

func defaultWeirdStatsFactSettings() weirdStatsFactSettings {
	settings := make(weirdStatsFactSettings, len(weirdStatsFactDefinitions))
	for _, def := range weirdStatsFactDefinitions {
		settings[def.ID] = weirdStatsFactSetting{
			Enabled:          def.DefaultEnabled,
			AutoPostEveryRun: def.DefaultAutoPostEveryRun,
		}
	}
	return settings
}

func weirdStatsFactEnabled(settings weirdStatsFactSettings, factID string) bool {
	if setting, ok := settings[factID]; ok {
		return setting.Enabled
	}
	if def, ok := weirdStatsFactDefinitionsByID[factID]; ok {
		return def.DefaultEnabled
	}
	return false
}

func weirdStatsFactAutoPostEveryRun(settings weirdStatsFactSettings, factID string) bool {
	if setting, ok := settings[factID]; ok {
		return setting.AutoPostEveryRun
	}
	if def, ok := weirdStatsFactDefinitionsByID[factID]; ok {
		return def.DefaultAutoPostEveryRun
	}
	return false
}

func buildUserFactPreferences(settings weirdStatsFactSettings) []storage.UserFactPreference {
	prefs := make([]storage.UserFactPreference, 0, len(weirdStatsFactDefinitions))
	for _, def := range weirdStatsFactDefinitions {
		prefs = append(prefs, storage.UserFactPreference{
			FactID:       def.ID,
			Enabled:      weirdStatsFactEnabled(settings, def.ID),
			PostToStrava: weirdStatsFactAutoPostEveryRun(settings, def.ID),
		})
	}
	return prefs
}

func filterWeirdStatsSnapshot(snapshot stats.StopStats, settings weirdStatsFactSettings) stats.StopStats {
	if !weirdStatsFactEnabled(settings, weirdStatsFactStopSummary) {
		snapshot.StopCount = 0
		snapshot.StopTotalSeconds = 0
	}
	if !weirdStatsFactEnabled(settings, weirdStatsFactTrafficLightStops) {
		snapshot.TrafficLightStopCount = 0
	}
	if !weirdStatsFactEnabled(settings, weirdStatsFactRoadCrossings) {
		snapshot.RoadCrossingCount = 0
	}
	return snapshot
}
