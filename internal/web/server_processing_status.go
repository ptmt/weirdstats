package web

func activityProcessingLabel(status string) string {
	switch status {
	case "analysis_pending":
		return "Analysis pending"
	case "analysis_error":
		return "Analysis unavailable"
	case "maps_pending":
		return "Map details pending"
	case "maps_error":
		return "Map details unavailable"
	default:
		return ""
	}
}
