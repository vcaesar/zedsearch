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

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/agent/mcp"
)

func testServer() http.Handler {
	return handler(mcp.NewServer(mcp.NewClient("http://127.0.0.1:1", "", ""), mcp.Options{}))
}

func TestHandler_CrossOrigin(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	tests := []struct {
		name   string
		site   string
		status int
	}{
		{"cross-site browser request", "cross-site", http.StatusForbidden},
		{"non-browser client", "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tt.site != "" {
				req.Header.Set("Sec-Fetch-Site", tt.site)
			}
			rec := httptest.NewRecorder()
			testServer().ServeHTTP(rec, req)
			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
		})
	}
}

func TestRun(t *testing.T) {
	server := mcp.NewServer(mcp.NewClient("http://127.0.0.1:1", "", ""), mcp.Options{})
	assert.ErrorContains(t, run(context.Background(), server, "grpc", ""), "unknown transport")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.NoError(t, run(ctx, server, "http", "127.0.0.1:0"), "http transport shuts down with ctx")
	assert.Error(t, run(context.Background(), server, "http", "bad-addr"))
}
