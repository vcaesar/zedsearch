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
 */

package tracing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	parentTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	parent      = "00-" + parentTrace + "-00f067aa0ba902b7-01"
)

// record makes the package tracer record every span for the test.
func record(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	saved := tracer
	tracer = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer(instrumentation)
	t.Cleanup(func() { tracer = saved })
	return rec
}

func headers(h map[string]string) func(string) string {
	return func(key string) string { return h[strings.ToLower(key)] }
}

func TestExtract(t *testing.T) {
	ctx := Extract(context.Background(), headers(map[string]string{"traceparent": parent}))
	assert.Equal(t, parentTrace, TraceID(ctx))

	assert.Empty(t, TraceID(Extract(context.Background(), headers(nil))))
	assert.Empty(t, TraceID(Extract(context.Background(), headers(map[string]string{"traceparent": "garbage"}))))
}

func TestDefaultTracerOnlyHandsOutIDs(t *testing.T) {
	_, span := Tracer().Start(context.Background(), "root")
	defer span.End()
	assert.False(t, span.IsRecording())
	assert.True(t, span.SpanContext().IsValid())
	assert.False(t, span.SpanContext().IsSampled())

	ctx := Extract(context.Background(), headers(map[string]string{"traceparent": parent}))
	_, child := Tracer().Start(ctx, "child")
	defer child.End()
	assert.Equal(t, parentTrace, child.SpanContext().TraceID().String())
}

func TestHook(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Hook(Hook{})

	ctx := Extract(context.Background(), headers(map[string]string{"traceparent": parent}))
	logger.Info().Ctx(ctx).Msg("traced")
	logger.Info().Msg("plain")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"trace_id":"`+parentTrace+`"`)
	assert.NotContains(t, lines[1], "trace_id")
}

func TestTransport(t *testing.T) {
	rec := record(t)
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "busy")
	}))
	defer srv.Close()

	ctx := Extract(context.Background(), headers(map[string]string{"traceparent": parent}))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/_internal/search", nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: Transport(nil)}).Do(req)
	require.NoError(t, err)

	assert.Empty(t, req.Header.Get("traceparent"), "caller's request must stay untouched")
	sc := trace.SpanContextFromContext(Extract(context.Background(), got.Get))
	assert.Equal(t, parentTrace, sc.TraceID().String())
	assert.NotEqual(t, "00f067aa0ba902b7", sc.SpanID().String(), "downstream parent is the client span")

	assert.Empty(t, rec.Ended(), "span lasts until the body is closed")
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, resp.Body.Close(), "closing twice ends the span once")

	spans := rec.Ended()
	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal(t, "POST /api/_internal/search", span.Name())
	assert.Equal(t, trace.SpanKindClient, span.SpanKind())
	assert.Equal(t, sc.SpanID(), span.SpanContext().SpanID())
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Contains(t, span.Attributes(), attribute.Int("http.response.status_code", http.StatusServiceUnavailable))
}

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("refused") }

func TestTransportError(t *testing.T) {
	rec := record(t)
	req := httptest.NewRequest(http.MethodGet, "http://node-2:4080/api/_internal/replication/recover", nil)
	_, err := Transport(failing{}).RoundTrip(req)
	require.EqualError(t, err, "refused")

	spans := rec.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.Equal(t, "refused", spans[0].Status().Description)
	require.Len(t, spans[0].Events(), 1)
	assert.Equal(t, "exception", spans[0].Events()[0].Name)
}

func TestStartDisabled(t *testing.T) {
	saved := tracer
	stop, err := Start(context.Background(), Config{Endpoint: "::not a url"})
	require.NoError(t, err)
	assert.Same(t, saved, tracer)
	assert.NoError(t, stop(context.Background()))
}

func TestStartInvalidEndpoint(t *testing.T) {
	_, err := Start(context.Background(), Config{Enable: true, Endpoint: "not a url"})
	assert.ErrorContains(t, err, "invalid endpoint")
}

func TestStartExports(t *testing.T) {
	saved := tracer
	t.Cleanup(func() { tracer = saved })

	var exported atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" && r.Header.Get("Content-Type") == "application/x-protobuf" {
			exported.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	stop, err := Start(context.Background(), Config{
		Enable: true, Endpoint: collector.URL + "/v1/traces",
		Service: "zedsearch", Version: "test", Node: "node-1",
	})
	require.NoError(t, err)

	_, span := Tracer().Start(context.Background(), "GET /healthz")
	assert.True(t, span.IsRecording())
	span.End()

	require.NoError(t, stop(context.Background()))
	assert.Equal(t, int32(1), exported.Load())
}
