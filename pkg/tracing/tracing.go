/* Copyright 2026 AtomAI.
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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
	tracer   = sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample())).Tracer(instrumentation)
	hookOnce sync.Once
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
	hookOnce.Do(func() { log.Logger = log.Logger.Hook(Hook{}) })
	if !cfg.Enable {
		return func(context.Context) error { return nil }, nil
	}

	var scheme string
	if cfg.Endpoint != "" {
		// the error must not echo the endpoint, it may hold credentials
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return nil, errors.New("tracing: invalid endpoint, want an http or https URL with a host")
		}
		scheme = u.Scheme
	}
	if otlpEnv("HEADERS") != "" && plainHTTP(scheme) {
		return nil, errors.New("tracing: OTEL_EXPORTER_OTLP_HEADERS need an https endpoint")
	}
	client, err := exportClient()
	if err != nil {
		return nil, err
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithHTTPClient(client)}
	if cfg.Endpoint != "" {
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
	if errors.Is(err, resource.ErrPartialResource) || errors.Is(err, resource.ErrSchemaURLConflict) {
		log.Warn().Err(err).Msg("tracing: incomplete resource")
	} else if err != nil {
		return nil, fmt.Errorf("tracing: resource: %w", err)
	}

	// sampling follows the caller, OTEL_TRACES_SAMPLER can change it
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	tracer = provider.Tracer(instrumentation)
	return provider.Shutdown, nil
}

// otlpEnv reads OTEL_EXPORTER_OTLP_TRACES_<key>, then OTEL_EXPORTER_OTLP_<key>.
func otlpEnv(key string) string {
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_" + key)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_" + key))
}

// plainHTTP reports whether spans are sent without TLS, following the
// precedence of otlptracehttp: the scheme of Config.Endpoint, then the
// insecure flag, then the endpoint of the environment, then https.
func plainHTTP(scheme string) bool {
	if scheme != "" {
		return scheme != "https"
	}
	if v := otlpEnv("INSECURE"); v != "" {
		return strings.EqualFold(v, "true")
	}
	if v := otlpEnv("ENDPOINT"); v != "" {
		u, err := url.Parse(v)
		return err != nil || u.Scheme == "http" || u.Scheme == "unix"
	}
	return false
}

// exportClient never follows redirects, so the export headers only reach
// the collector. otlptracehttp ignores its own TLS and timeout settings
// once given a client, so they are read from the environment here.
func exportClient() (*http.Client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if file := otlpEnv("CERTIFICATE"); file != "" {
		pem, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("tracing: OTEL_EXPORTER_OTLP_CERTIFICATE: %w", err)
		}
		tlsCfg.RootCAs = x509.NewCertPool()
		if !tlsCfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, errors.New("tracing: OTEL_EXPORTER_OTLP_CERTIFICATE: no PEM certificate")
		}
	}
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_TRACES_", "OTEL_EXPORTER_OTLP_"} {
		cert, key := strings.TrimSpace(os.Getenv(prefix+"CLIENT_CERTIFICATE")), strings.TrimSpace(os.Getenv(prefix+"CLIENT_KEY"))
		if cert == "" || key == "" {
			continue
		}
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("tracing: %sCLIENT_CERTIFICATE: %w", prefix, err)
		}
		tlsCfg.Certificates = []tls.Certificate{pair}
		break
	}

	timeout := 10 * time.Second
	if v := otlpEnv("TIMEOUT"); v != "" {
		if ms, err := strconv.Atoi(v); err != nil {
			log.Warn().Err(err).Msg("tracing: OTEL_EXPORTER_OTLP_TIMEOUT is not in milliseconds, using 10s")
		} else {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
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
	// http.Client replaces a nil body with http.NoBody, keep it visible
	if resp.Body == nil {
		span.End()
		return resp, nil
	}
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
