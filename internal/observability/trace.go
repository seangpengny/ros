package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// SpanContext is the small W3C Trace Context subset carried by this service.
// The API creates a child span for every request and preserves the trace ID
// when a caller supplies a valid traceparent header.
type SpanContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Flags        string
}

type spanContextKey struct{}

// Configure installs the process tracer provider. OTLP export is enabled only
// when an endpoint is configured; the returned shutdown function is safe to
// call in both collector and local-development modes.
func Configure(ctx context.Context, serviceName string) (shutdown func(context.Context) error, enabled bool, err error) {
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", serviceName)))
	if err != nil {
		return nil, false, err
	}
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint == "" {
		provider := sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		otel.SetTracerProvider(provider)
		return provider.Shutdown, false, nil
	}
	exporterOptions, err := otlpHTTPOptions(endpoint)
	if err != nil {
		return nil, false, err
	}
	exporter, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		return nil, false, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, true, nil
}

func otlpHTTPOptions(endpoint string) ([]otlptracehttp.Option, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	host := parsed.Host
	if host == "" {
		host = parsed.Path
	}
	if host == "" {
		return nil, &url.Error{Op: "parse", URL: endpoint, Err: errors.New("endpoint host is required")}
	}
	options := []otlptracehttp.Option{otlptracehttp.WithEndpoint(host)}
	if parsed.Scheme == "http" || strings.EqualFold(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"), "true") {
		options = append(options, otlptracehttp.WithInsecure())
	}
	if parsed.Path != "" && parsed.Path != "/" {
		options = append(options, otlptracehttp.WithURLPath(parsed.Path))
	}
	return options, nil
}

func Extract(ctx context.Context, headers http.Header) context.Context {
	return propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(headers))
}

func StartSpan(ctx context.Context, name string) (context.Context, oteltrace.Span) {
	return otel.Tracer("roslegacy/api").Start(ctx, name, oteltrace.WithSpanKind(oteltrace.SpanKindServer))
}

func SpanContextFor(span oteltrace.Span) oteltrace.SpanContext {
	if span == nil || !span.IsRecording() {
		return oteltrace.SpanContext{}
	}
	return span.SpanContext()
}

func AnnotateHTTPSpan(ctx context.Context, method, path string, status int, duration time.Duration) {
	span := oteltrace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(
		attribute.String("http.request.method", method),
		attribute.String("url.path", path),
		attribute.Int("http.response.status_code", status),
		attribute.Int64("http.request.duration_ms", duration.Milliseconds()),
	)
	if status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(status))
	}
}

func Start(ctx context.Context, traceparent string, childContext ...oteltrace.SpanContext) (context.Context, SpanContext) {
	parent, ok := parseTraceparent(traceparent)
	span := SpanContext{TraceID: randomHex(16), SpanID: randomHex(8), Flags: "01"}
	if len(childContext) > 0 && childContext[0].IsValid() {
		otelSpanContext := childContext[0]
		span.TraceID = otelSpanContext.TraceID().String()
		span.SpanID = otelSpanContext.SpanID().String()
		span.Flags = otelSpanContext.TraceFlags().String()
	}
	if ok {
		span.TraceID = parent.TraceID
		span.ParentSpanID = parent.SpanID
		if len(childContext) == 0 || !childContext[0].IsValid() {
			span.Flags = parent.Flags
		}
	}
	return context.WithValue(ctx, spanContextKey{}, span), span
}

func FromContext(ctx context.Context) (SpanContext, bool) {
	value, ok := ctx.Value(spanContextKey{}).(SpanContext)
	return value, ok
}

func (span SpanContext) Traceparent() string {
	return "00-" + span.TraceID + "-" + span.SpanID + "-" + span.Flags
}

func parseTraceparent(value string) (SpanContext, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return SpanContext{}, false
	}
	if !isHex(parts[1]) || !isHex(parts[2]) || !isHex(parts[3]) || strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return SpanContext{}, false
	}
	return SpanContext{TraceID: strings.ToLower(parts[1]), SpanID: strings.ToLower(parts[2]), Flags: strings.ToLower(parts[3])}, true
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func randomHex(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		// crypto/rand failures are process-level faults in normal operation. A
		// zero value is still preferable to emitting malformed trace headers.
		return strings.Repeat("1", size*2)
	}
	return hex.EncodeToString(value)
}
