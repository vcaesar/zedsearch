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
 */

package tracing

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
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

// isolate clears the OTel settings of the environment that Start reads
// and pins the sampler and protocol the export tests rely on.
func isolate(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ENDPOINT", "INSECURE", "HEADERS", "CERTIFICATE", "CLIENT_CERTIFICATE", "CLIENT_KEY", "TIMEOUT"} {
		t.Setenv("OTEL_EXPORTER_OTLP_"+key, "")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_"+key, "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	t.Setenv("OTEL_SERVICE_NAME", "")
	saved := tracer
	t.Cleanup(func() { tracer = saved })
}

// writePEM stores the PEM block in dir and returns its path.
func writePEM(t *testing.T, dir, name, typ string, der []byte) string {
	t.Helper()
	file := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600))
	return file
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name, traceparent, want string
	}{
		{"valid", parent, parentTrace},
		{"missing", "", ""},
		{"malformed", "garbage", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := map[string]string{}
			if tt.traceparent != "" {
				h["traceparent"] = tt.traceparent
			}
			assert.Equal(t, tt.want, TraceID(Extract(context.Background(), headers(h))))
		})
	}
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
	gotHeader := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader <- r.Header.Clone()
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
	got := <-gotHeader
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

// noBody answers like a RoundTripper that leaves Body nil.
type noBody struct{}

func (noBody) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}}, nil
}

func TestTransportNilBody(t *testing.T) {
	rec := record(t)
	req, err := http.NewRequest(http.MethodDelete, "http://node-2:4080/api/_internal/doc", nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: Transport(noBody{})}).Do(req)
	require.NoError(t, err)

	spans := rec.Ended()
	require.Len(t, spans, 1, "nothing to close, the span ends right away")
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, b)
	require.NoError(t, resp.Body.Close())
	assert.Contains(t, spans[0].Attributes(), attribute.Int("http.response.status_code", http.StatusNoContent))
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

func TestStartInstallsHookOnce(t *testing.T) {
	isolate(t)
	saved := log.Logger
	t.Cleanup(func() { log.Logger = saved })
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf)
	hookOnce = sync.Once{}
	ctx := Extract(context.Background(), headers(map[string]string{"traceparent": parent}))

	stop, err := Start(context.Background(), Config{})
	require.NoError(t, err)
	require.NoError(t, stop(context.Background()))
	log.Info().Ctx(ctx).Msg("disabled")
	assert.Contains(t, buf.String(), `"trace_id":"`+parentTrace+`"`, "the hook runs without export")

	_, err = Start(context.Background(), Config{Enable: true, Endpoint: "not a url"})
	require.Error(t, err)
	buf.Reset()
	log.Info().Ctx(ctx).Msg("retried")
	assert.Equal(t, 1, strings.Count(buf.String(), "trace_id"))
}

func TestStartInvalidEndpoint(t *testing.T) {
	isolate(t)
	for _, endpoint := range []string{
		"not a url",
		"/v1/traces",
		"localhost:4318/v1/traces",
		"ftp://collector:4318/v1/traces",
		"http:///v1/traces",
		"https://user:s3cret@collector:port/v1/traces",
	} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := Start(context.Background(), Config{Enable: true, Endpoint: endpoint})
			require.ErrorContains(t, err, "invalid endpoint")
			assert.NotContains(t, err.Error(), "s3cret")
			assert.NotContains(t, err.Error(), endpoint)
		})
	}
}

func TestStartHeadersNeedTLS(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		env      map[string]string
		wantErr  bool
	}{
		{"http endpoint", "http://collector:4318/v1/traces", map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "X-API-Key=k"}, true},
		{"loopback http endpoint", "http://127.0.0.1:4318/v1/traces", map[string]string{"OTEL_EXPORTER_OTLP_TRACES_HEADERS": "X-API-Key=k"}, true},
		{"http traces endpoint env", "", map[string]string{
			"OTEL_EXPORTER_OTLP_HEADERS":         "X-API-Key=k",
			"OTEL_EXPORTER_OTLP_ENDPOINT":        "https://collector:4318",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://collector:4318/v1/traces",
		}, true},
		{"http endpoint env", "", map[string]string{
			"OTEL_EXPORTER_OTLP_HEADERS":  "X-API-Key=k",
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318",
		}, true},
		{"insecure env over https endpoint env", "", map[string]string{
			"OTEL_EXPORTER_OTLP_HEADERS":         "X-API-Key=k",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://collector:4318/v1/traces",
			"OTEL_EXPORTER_OTLP_TRACES_INSECURE": "true",
		}, true},
		{"traces insecure env wins", "", map[string]string{
			"OTEL_EXPORTER_OTLP_HEADERS":         "X-API-Key=k",
			"OTEL_EXPORTER_OTLP_INSECURE":        "false",
			"OTEL_EXPORTER_OTLP_TRACES_INSECURE": "TRUE",
		}, true},
		{"https endpoint over insecure env", "https://collector:4318/v1/traces", map[string]string{
			"OTEL_EXPORTER_OTLP_HEADERS":  "X-API-Key=k",
			"OTEL_EXPORTER_OTLP_INSECURE": "true",
		}, false},
		{"https endpoint env", "", map[string]string{
			"OTEL_EXPORTER_OTLP_HEADERS":  "X-API-Key=k",
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector:4318",
		}, false},
		{"default https endpoint", "", map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "X-API-Key=k"}, false},
		{"no headers over http", "http://localhost:4318/v1/traces", nil, false},
		{"no headers over http env", "", map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			stop, err := Start(context.Background(), Config{Enable: true, Endpoint: tt.endpoint})
			if tt.wantErr {
				require.EqualError(t, err, "tracing: OTEL_EXPORTER_OTLP_HEADERS need an https endpoint")
				return
			}
			require.NoError(t, err)
			require.NoError(t, stop(context.Background()))
		})
	}
}

func TestExportClient(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certFile := writePEM(t, dir, "client.pem", "CERTIFICATE", der)
	keyFile := writePEM(t, dir, "client.key", "EC PRIVATE KEY", keyDER)
	garbage := filepath.Join(dir, "garbage.pem")
	require.NoError(t, os.WriteFile(garbage, []byte("not pem"), 0o600))

	tests := []struct {
		name        string
		env         map[string]string
		wantTimeout time.Duration
		wantCA      bool
		wantCerts   int
		wantErr     string
	}{
		{name: "defaults", wantTimeout: 10 * time.Second},
		{name: "timeout", env: map[string]string{"OTEL_EXPORTER_OTLP_TIMEOUT": "250"}, wantTimeout: 250 * time.Millisecond},
		{name: "traces timeout wins", env: map[string]string{
			"OTEL_EXPORTER_OTLP_TIMEOUT": "250", "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT": "500",
		}, wantTimeout: 500 * time.Millisecond},
		{name: "bad timeout keeps default", env: map[string]string{"OTEL_EXPORTER_OTLP_TIMEOUT": "5s"}, wantTimeout: 10 * time.Second},
		{name: "ca", env: map[string]string{"OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE": certFile}, wantTimeout: 10 * time.Second, wantCA: true},
		{name: "client cert", env: map[string]string{
			"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE": certFile, "OTEL_EXPORTER_OTLP_CLIENT_KEY": keyFile,
		}, wantTimeout: 10 * time.Second, wantCerts: 1},
		{name: "client cert without key", env: map[string]string{
			"OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE": certFile,
		}, wantTimeout: 10 * time.Second},
		{name: "missing ca", env: map[string]string{"OTEL_EXPORTER_OTLP_CERTIFICATE": filepath.Join(dir, "missing.pem")}, wantErr: "OTEL_EXPORTER_OTLP_CERTIFICATE"},
		{name: "ca not pem", env: map[string]string{"OTEL_EXPORTER_OTLP_CERTIFICATE": garbage}, wantErr: "no PEM certificate"},
		{name: "bad client key", env: map[string]string{
			"OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE": certFile, "OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY": garbage,
		}, wantErr: "OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			client, err := exportClient()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantTimeout, client.Timeout)
			tr, ok := client.Transport.(*http.Transport)
			require.True(t, ok)
			assert.NotNil(t, tr.Proxy, "proxy still follows the environment")
			assert.Equal(t, tt.wantCA, tr.TLSClientConfig.RootCAs != nil)
			assert.Len(t, tr.TLSClientConfig.Certificates, tt.wantCerts)
			assert.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
		})
	}
}

func TestStartDoesNotFollowRedirects(t *testing.T) {
	isolate(t)
	var elsewhere atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	gotKey := make(chan string, 1)
	collector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey <- r.Header.Get("X-API-Key")
		http.Redirect(w, r, other.URL+"/v1/traces", http.StatusTemporaryRedirect)
	}))
	defer collector.Close()

	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "X-API-Key=secret")
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", writePEM(t, t.TempDir(), "ca.pem", "CERTIFICATE", collector.Certificate().Raw))
	stop, err := Start(context.Background(), Config{Enable: true, Endpoint: collector.URL + "/v1/traces"})
	require.NoError(t, err)

	_, span := Tracer().Start(context.Background(), "GET /healthz")
	span.End()
	require.NoError(t, stop(context.Background()))

	assert.Equal(t, "secret", <-gotKey, "headers reach the collector over https")
	assert.Zero(t, elsewhere.Load(), "the redirect is not followed")
}

func TestStartKeepsPartialResource(t *testing.T) {
	isolate(t)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=test,")

	// the SDK returns the usable resource together with the error
	res, err := resource.New(context.Background(), resource.WithFromEnv())
	require.ErrorIs(t, err, resource.ErrPartialResource)
	env, ok := res.Set().Value("deployment.environment")
	require.True(t, ok)
	assert.Equal(t, "test", env.AsString())

	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	stop, err := Start(context.Background(), Config{Enable: true, Endpoint: collector.URL + "/v1/traces", Service: "zedsearch"})
	require.NoError(t, err)

	_, span := Tracer().Start(context.Background(), "GET /healthz")
	span.End()
	ro, ok := span.(sdktrace.ReadOnlySpan)
	require.True(t, ok)
	attrs := ro.Resource().Set()
	env, ok = attrs.Value("deployment.environment")
	require.True(t, ok)
	assert.Equal(t, "test", env.AsString())
	svc, ok := attrs.Value("service.name")
	require.True(t, ok)
	assert.Equal(t, "zedsearch", svc.AsString())
	require.NoError(t, stop(context.Background()))
}

func TestStartExports(t *testing.T) {
	isolate(t)

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
