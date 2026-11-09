package maps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"
)

// ResponseError intentionally excludes the request URL and provider body.
type ResponseError struct{ StatusCode int }

func (e *ResponseError) Error() string { return fmt.Sprintf("overpass status %d", e.StatusCode) }

// SafeError returns only allowlisted diagnostics suitable for routine logs.
func SafeError(err error) (string, int) {
	var response *ResponseError
	switch {
	case errors.As(err, &response):
		return "http_status", response.StatusCode
	case errors.Is(err, context.Canceled):
		return "canceled", 0
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused", 0
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", 0
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns", 0
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "timeout", 0
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "invalid_response", 0
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		return "transport", 0
	}
	return "unknown", 0
}
