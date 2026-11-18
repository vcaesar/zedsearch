/* Copyright 2026 AtomAI
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
or production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

// Package tracing follows a request across nodes with OpenTelemetry.
//
// Every request gets a W3C trace id: it is logged, returned to the client
// and passed on to the nodes the request reaches. Spans are only recorded
// and exported over OTLP/HTTP when tracing is enabled.
package tracing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	// TraceIDHeader returns the trace id to the client.
	TraceIDHeader = "X-Trace-Id"

	instrumentation = "github.com/vcaesar/zedsearch"
)

var (
	propagator = propagation.TraceContext{}
	// the default records nothing but still hands out trace ids
	tracer = sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample())).Tracer(instrumentation)
)

// Config selects where spans go.
type Config struct {
	// Enable records and exports spans.
	Enable bool
	// Endpoint is the full OTLP/HTTP traces URL, e.g.
	// http://localhost:4318/v1/traces. Empty uses the
	// OTEL_EXPORTER_OTLP_* environment, then localhost:4318.
	Endpoint string
	// Service and Version name this process, Node tells cluster nodes apart.
	Service, Version, Node string
}

// Start sets up tracing, it must run before the server serves requests.
// The returned function flushes buffered spans on shutdown.
func Start(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	log.Logger = log.Logger.Hook(Hook{})
	if !cfg.Enable {
		return func(context.Context) error { return nil }, nil
	}

	var opts []otlptracehttp.Option
	if cfg.Endpoint != "" {
		if _, err := url.ParseRequestURI(cfg.Endpoint); err != nil {
			return nil, fmt.Errorf("tracing: invalid endpoint: %w", err)
		}
		opts = append(opts, otlptracehttp.WithEndpointURL(cfg.Endpoint))
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("tracing: otlp exporter: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", cfg.Service),
		attribute.String("service.version", cfg.Version),
	}
	if cfg.Node != "" {
		attrs = append(attrs, attribute.String("service.instance.id", cfg.Node))
	}
	// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES override the defaults
	res, err := resource.New(ctx, resource.WithAttributes(attrs...), resource.WithHost(), resource.WithFromEnv())
	if err != nil {
		return nil, fmt.Errorf("tracing: resource: %w", err)
	}

	// sampling follows the caller, OTEL_TRACES_SAMPLER can change it
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	tracer = provider.Tracer(instrumentation)
	return provider.Shutdown, nil
}

// Tracer creates the spans of this process.
func Tracer() trace.Tracer { return tracer }

// Extract continues the trace the caller sent in its headers.
func Extract(ctx context.Context, header func(string) string) context.Context {
	return propagator.Extract(ctx, getter(header))
}

// getter reads the propagation headers of an incoming request.
type getter func(string) string

func (g getter) Get(key string) string { return g(key) }

func (g getter) Set(string, string) {}

func (g getter) Keys() []string { return propagator.Fields() }

// TraceID returns the trace id of ctx, empty when there is none.
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

// Hook adds the trace id to log events given a context with Event.Ctx.
type Hook struct{}

func (Hook) Run(e *zerolog.Event, _ zerolog.Level, _ string) {
	if id := TraceID(e.GetCtx()); id != "" {
		e.Str("trace_id", id)
	}
}

// Transport traces the requests sent to other nodes: one client span per
// request, ended when the response body is closed, and the trace context
// in the request headers. A nil base uses http.DefaultTransport.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base}
}

type transport struct {
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := tracer.Start(req.Context(), req.Method+" "+req.URL.Path, trace.WithSpanKind(trace.SpanKindClient))
	if span.IsRecording() {
		span.SetAttributes(
			attribute.String("http.request.method", req.Method),
			attribute.String("server.address", req.URL.Host),
			attribute.String("url.path", req.URL.Path),
		)
	}
	// a RoundTripper must not modify the caller's request
	req = req.Clone(ctx)
	propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}
	SetStatus(span, resp.StatusCode)
	resp.Body = &spanBody{ReadCloser: resp.Body, span: span}
	return resp, nil
}

// SetStatus records an HTTP status code on span, 5xx marks it failed.
func SetStatus(span trace.Span, code int) {
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(attribute.Int("http.response.status_code", code))
	if code >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(code))
	}
}

// spanBody ends the client span once the caller is done with the body.
type spanBody struct {
	io.ReadCloser
	span trace.Span
	once sync.Once
}

func (b *spanBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.span.End() })
	return err
}
