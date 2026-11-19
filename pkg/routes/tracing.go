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

package routes

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/vcaesar/zedsearch/pkg/tracing"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

// Tracing runs every request in a server span, continuing the trace of a
// `traceparent` header, and returns the trace id in X-Trace-Id. It must be
// the first middleware so the others log the trace id.
func Tracing(app *zhttp.Engine) {
	app.Use(func(c zhttp.Context) {
		route := c.FullPath()
		if route == "" {
			route = unmatchedRoute
		}
		ctx := tracing.Extract(c.Context(), c.GetHeader)
		ctx, span := tracing.Tracer().Start(ctx, c.Method()+" "+route, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		if span.IsRecording() {
			span.SetAttributes(
				attribute.String("http.request.method", c.Method()),
				attribute.String("http.route", route),
				attribute.String("url.path", c.Path()),
			)
		}
		c.SetContext(ctx)
		c.Header(tracing.TraceIDHeader, span.SpanContext().TraceID().String())

		c.Next()

		tracing.SetStatus(span, c.ResponseStatus())
	})
}
