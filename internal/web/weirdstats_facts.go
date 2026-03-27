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
	ID          string
	Label       string
	Description string
	Enabled     bool
	RideOnly    bool
}

type weirdStatsFactDefinition struct {
	ID             string
	Label          string
	Description    string
	DefaultEnabled bool
	RideOnly       bool
}

var weirdStatsFactDefinitions = []weirdStatsFactDefinition{
	{
		ID:             weirdStatsFactStopSummary,
		Label:          "Stop summary",
		Description:    "Write detected stop count and total stopped time.",
		DefaultEnabled: true,
	},
	{
		ID:             weirdStatsFactTrafficLightStops,
		Label:          "Traffic-light stops",
		Description:    "Write how many detected stops happened near traffic lights.",
		DefaultEnabled: true,
	},
	{
		ID:             weirdStatsFactLongestSegment,
		Label:          "Longest segment",
		Description:    "Write the longest moving segment with distance, speed, and power when available.",
		DefaultEnabled: true,
		RideOnly:       true,
	},
	{
		ID:             weirdStatsFactCoffeeStop,
		Label:          "Coffee stop",
		Description:    "Write the best detected cafe or food stop name.",
		DefaultEnabled: true,
		RideOnly:       true,
	},
	{
		ID:             weirdStatsFactRouteHighlights,
		Label:          "Route highlights",
		Description:    "Write notable landmarks detected near the route.",
		DefaultEnabled: true,
		RideOnly:       true,
	},
	{
		ID:             weirdStatsFactRoadCrossings,
		Label:          "Road crossings",
		Description:    "Write detected road crossings after stops, including road names when available.",
		DefaultEnabled: true,
	},
	{
		ID:             weirdStatsFactAcceleration030,
		Label:          "0 to 30 km/h",
		Description:    "Write the fastest detected acceleration from standstill to 30 km/h.",
		DefaultEnabled: true,
		RideOnly:       true,
	},
	{
		ID:             weirdStatsFactAcceleration040,
		Label:          "0 to 40 km/h",
		Description:    "Write the fastest detected acceleration from standstill to 40 km/h.",
		DefaultEnabled: true,
		RideOnly:       true,
	},
	{
		ID:             weirdStatsFactDeceleration400,
		Label:          "40 to 0 km/h",
		Description:    "Write the fastest detected deceleration from 40 km/h to standstill.",
		DefaultEnabled: true,
		RideOnly:       true,
	},
	{
		ID:             weirdStatsFactDeceleration300,
		Label:          "30 to 0 km/h",
		Description:    "Write the fastest detected deceleration from 30 km/h to standstill.",
		DefaultEnabled: true,
		RideOnly:       true,
	},
}

var weirdStatsFactDefinitionsByID = func() map[string]weirdStatsFactDefinition {
	items := make(map[string]weirdStatsFactDefinition, len(weirdStatsFactDefinitions))
	for _, item := range weirdStatsFactDefinitions {
		items[item.ID] = item
	}
	return items
}()

func (s *Server) loadWeirdStatsFactSettings(ctx context.Context, userID int64) (map[string]bool, error) {
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
		settings[pref.FactID] = pref.Enabled
	}
	return settings, nil
}

func buildSettingsFacts(settings map[string]bool) []SettingsFact {
	facts := make([]SettingsFact, 0, len(weirdStatsFactDefinitions))
	for _, def := range weirdStatsFactDefinitions {
		facts = append(facts, SettingsFact{
			ID:          def.ID,
			Label:       def.Label,
			Description: def.Description,
			Enabled:     weirdStatsFactEnabled(settings, def.ID),
			RideOnly:    def.RideOnly,
		})
	}
	return facts
}

func defaultWeirdStatsFactSettings() map[string]bool {
	settings := make(map[string]bool, len(weirdStatsFactDefinitions))
	for _, def := range weirdStatsFactDefinitions {
		settings[def.ID] = def.DefaultEnabled
	}
	return settings
}

func weirdStatsFactEnabled(settings map[string]bool, factID string) bool {
	if enabled, ok := settings[factID]; ok {
		return enabled
	}
	if def, ok := weirdStatsFactDefinitionsByID[factID]; ok {
		return def.DefaultEnabled
	}
	return false
}

func buildUserFactPreferences(settings map[string]bool) []storage.UserFactPreference {
	prefs := make([]storage.UserFactPreference, 0, len(weirdStatsFactDefinitions))
	for _, def := range weirdStatsFactDefinitions {
		prefs = append(prefs, storage.UserFactPreference{
			FactID:  def.ID,
			Enabled: weirdStatsFactEnabled(settings, def.ID),
		})
	}
	return prefs
}

func filterWeirdStatsSnapshot(snapshot stats.StopStats, settings map[string]bool) stats.StopStats {
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
