package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"roslegacy/internal/observability"
)

func TestRequestContextMiddlewarePropagatesTraceContext(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	recorder := httptest.NewRecorder()
	requestContextMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		span, ok := observability.FromContext(request.Context())
		if !ok || span.TraceID != "0123456789abcdef0123456789abcdef" || span.ParentSpanID != "0123456789abcdef" {
			t.Fatalf("trace context was not continued: %+v ok=%v", span, ok)
		}
		writer.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if recorder.Header().Get("X-Request-ID") == "" || recorder.Header().Get("X-Trace-ID") != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("correlation headers missing: %v", recorder.Header())
	}
	if got := recorder.Header().Get("traceparent"); got == request.Header.Get("traceparent") || got == "" {
		t.Fatalf("response traceparent was not a child span: %q", got)
	}
}
