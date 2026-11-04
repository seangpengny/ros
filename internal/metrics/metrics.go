package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Registry struct {
	requests    atomic.Uint64
	requestErrs atomic.Uint64
	durationNS  atomic.Uint64
	maxDuration atomic.Uint64
	active      atomic.Int64
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) ObserveRequest(status int) {
	r.ObserveRequestDuration(status, 0)
}

func (r *Registry) ObserveRequestDuration(status int, duration time.Duration) {
	r.requests.Add(1)
	if status >= http.StatusInternalServerError {
		r.requestErrs.Add(1)
	}
	nanoseconds := uint64(duration)
	r.durationNS.Add(nanoseconds)
	for {
		current := r.maxDuration.Load()
		if nanoseconds <= current || r.maxDuration.CompareAndSwap(current, nanoseconds) {
			break
		}
	}
}

func (r *Registry) SetActive(value int64) { r.active.Store(value) }

func (r *Registry) Prometheus() string {
	return fmt.Sprintf("# HELP ros_http_requests_total Total HTTP requests.\n# TYPE ros_http_requests_total counter\nros_http_requests_total %d\n# HELP ros_http_request_errors_total HTTP requests returning 5xx.\n# TYPE ros_http_request_errors_total counter\nros_http_request_errors_total %d\n# HELP ros_http_request_duration_seconds_total Cumulative HTTP request duration.\n# TYPE ros_http_request_duration_seconds_total counter\nros_http_request_duration_seconds_total %.9f\n# HELP ros_http_request_duration_seconds_max Maximum HTTP request duration.\n# TYPE ros_http_request_duration_seconds_max gauge\nros_http_request_duration_seconds_max %.9f\n# HELP ros_active_sessions Current active sessions reported by the process.\n# TYPE ros_active_sessions gauge\nros_active_sessions %d\n", r.requests.Load(), r.requestErrs.Load(), float64(r.durationNS.Load())/float64(time.Second), float64(r.maxDuration.Load())/float64(time.Second), r.active.Load())
}
