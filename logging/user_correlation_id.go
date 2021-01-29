package logging

import "net/http"

var UserCorrelationIdHeader = "X-User-Correlation-Id"

// GetCorrelationId returns the correlation from of the request.
func GetUserCorrelationId(h http.Header) string {
	return h.Get(UserCorrelationIdHeader)
}
