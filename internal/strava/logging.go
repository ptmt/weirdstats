package strava

import (
	"log"
	"net/url"
	"strings"
)

func logRequest(method, endpoint string) {
	if method == "" && endpoint == "" {
		return
	}

	safe := endpoint
	if parsed, err := url.Parse(endpoint); err == nil {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		if parsed.Scheme != "" || parsed.Host != "" {
			safe = parsed.Scheme + "://" + parsed.Host + parsed.Path
		} else {
			safe = parsed.Path
		}
		if safe == "" {
			safe = parsed.String()
		}
	} else if idx := strings.Index(endpoint, "?"); idx >= 0 {
		safe = endpoint[:idx]
	}

	method = strings.ToUpper(strings.TrimSpace(method))
	switch {
	case method == "" && safe != "":
		log.Printf("strava request: %s", safe)
	case method != "" && safe == "":
		log.Printf("strava request: %s", method)
	default:
		log.Printf("strava request: %s %s", method, safe)
	}
}
