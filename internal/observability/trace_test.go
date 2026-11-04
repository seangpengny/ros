package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartCreatesRootTrace(t *testing.T) {
	ctx, span := Start(context.Background(), "")
	if span.TraceID == "" || span.SpanID == "" || span.ParentSpanID != "" {
		t.Fatalf("unexpected root span: %+v", span)
	}
	if got, ok := FromContext(ctx); !ok || got != span {
		t.Fatalf("span was not stored in context: got=%+v ok=%v", got, ok)
	}
	if !strings.HasPrefix(span.Traceparent(), "00-") {
		t.Fatalf("invalid traceparent: %s", span.Traceparent())
	}
}

func TestStartContinuesValidParentAndRejectsInvalidParent(t *testing.T) {
	_, child := Start(context.Background(), "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	if child.TraceID != "0123456789abcdef0123456789abcdef" || child.ParentSpanID != "0123456789abcdef" {
		t.Fatalf("valid parent was not continued: %+v", child)
	}
	_, root := Start(context.Background(), "not-a-traceparent")
	if root.ParentSpanID != "" || root.TraceID == "0123456789abcdef0123456789abcdef" {
		t.Fatalf("invalid parent was accepted: %+v", root)
	}
}

func TestConfigureExportsOTLPHTTP(t *testing.T) {
	var requests atomic.Int32
	received := make(chan struct{}, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/traces" {
			t.Errorf("unexpected OTLP request: %s %s", request.Method, request.URL.Path)
		}
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			t.Errorf("read OTLP request: %v", err)
		}
		requests.Add(1)
		received <- struct{}{}
		writer.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	shutdown, enabled, err := Configure(context.Background(), "observability-test")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("OTLP exporter was not enabled")
	}
	ctx, span := StartSpan(context.Background(), "test.span")
	if ctx == nil || !span.IsRecording() {
		t.Fatal("configured SDK did not create a recording span")
	}
	span.End()
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("OTLP collector did not receive an export")
	}
	if requests.Load() != 1 {
		t.Fatalf("received %d OTLP requests, want 1", requests.Load())
	}
}
