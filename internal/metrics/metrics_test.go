package metrics

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPrometheusCounters(t *testing.T) {
	registry := NewRegistry()
	registry.ObserveRequestDuration(http.StatusOK, 2*time.Millisecond)
	registry.ObserveRequest(http.StatusInternalServerError)
	registry.SetActive(3)
	output := registry.Prometheus()
	for _, expected := range []string{
		"ros_http_requests_total 2",
		"ros_http_request_errors_total 1",
		"ros_active_sessions 3",
		"ros_http_request_duration_seconds_total 0.002000000",
		"ros_http_request_duration_seconds_max 0.002000000",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics output missing %q: %s", expected, output)
		}
	}
}
