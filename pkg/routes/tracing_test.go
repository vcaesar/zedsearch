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

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/tracing"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

func TestTracing(t *testing.T) {
	var downstream string
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstream = tracing.TraceID(tracing.Extract(context.Background(), r.Header.Get))
	}))
	defer node.Close()

	var handlerID string
	e := zhttp.New()
	Tracing(e)
	e.GET("/api/:target/_search", func(c zhttp.Context) {
		handlerID = tracing.TraceID(c.Context())
		req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, node.URL, nil)
		require.NoError(t, err)
		resp, err := clusterClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		c.Status(http.StatusOK)
	})

	t.Run("continues the caller's trace", func(t *testing.T) {
		const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		req := httptest.NewRequest(http.MethodGet, "/api/logs/_search", nil)
		req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
		w := serve(e, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, traceID, w.Header().Get(tracing.TraceIDHeader))
		assert.Equal(t, traceID, handlerID)
		assert.Equal(t, traceID, downstream)
	})

	t.Run("starts a trace", func(t *testing.T) {
		w := serve(e, httptest.NewRequest(http.MethodGet, "/api/logs/_search", nil))
		id := w.Header().Get(tracing.TraceIDHeader)
		assert.Len(t, id, 32)
		assert.NotEqual(t, "00000000000000000000000000000000", id)
		assert.Equal(t, id, handlerID)
		assert.Equal(t, id, downstream)
	})
}

func BenchmarkTracing(b *testing.B) {
	for _, traced := range []bool{false, true} {
		e := zhttp.New()
		if traced {
			Tracing(e)
		}
		e.GET("/api/:target/_search", func(c zhttp.Context) { c.Status(http.StatusOK) })
		name := "off"
		if traced {
			name = "on"
		}
		b.Run(name, func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, "/api/logs/_search", nil)
			b.ReportAllocs()
			for b.Loop() {
				e.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}

func TestTracingUnmatchedRoute(t *testing.T) {
	e := zhttp.New()
	Tracing(e)
	w := serve(e, httptest.NewRequest(http.MethodGet, "/no/such/route", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Len(t, w.Header().Get(tracing.TraceIDHeader), 32)
}
