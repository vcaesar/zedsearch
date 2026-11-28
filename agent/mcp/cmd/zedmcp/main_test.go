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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/agent/mcp"
)

func testServer(token string) http.Handler {
	return handler(mcp.NewServer(mcp.NewClient("http://127.0.0.1:1", "", ""), mcp.Options{}), token)
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func initRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/", strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	return req
}

func TestHandler_CrossOrigin(t *testing.T) {
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
			req := initRequest()
			if tt.site != "" {
				req.Header.Set("Sec-Fetch-Site", tt.site)
			}
			rec := httptest.NewRecorder()
			testServer("").ServeHTTP(rec, req)
			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
		})
	}
}

func TestHandler_Token(t *testing.T) {
	tests := []struct {
		name   string
		auth   string
		status int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"basic", "Basic c2VjcmV0", http.StatusUnauthorized},
		{"valid", "Bearer secret", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := initRequest()
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			testServer("secret").ServeHTTP(rec, req)
			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
			if tt.status == http.StatusUnauthorized {
				assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
			}
		})
	}
}

func TestLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8090", true},
		{"localhost:8090", true},
		{"[::1]:8090", true},
		{"0.0.0.0:8090", false},
		{":8090", false},
		{"10.0.0.1:8090", false},
		{"bad-addr", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			assert.Equal(t, tt.want, loopback(tt.addr))
		})
	}
}

func TestRun(t *testing.T) {
	server := mcp.NewServer(mcp.NewClient("http://127.0.0.1:1", "", ""), mcp.Options{})
	assert.ErrorContains(t, run(context.Background(), server, "grpc", "", ""), "unknown transport")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.NoError(t, run(ctx, server, "http", "127.0.0.1:0", ""), "http transport shuts down with ctx")
	assert.NoError(t, run(ctx, server, "http", ":0", "secret"), "non-loopback with a token")
	assert.ErrorContains(t, run(context.Background(), server, "http", "0.0.0.0:0", ""), "ZED_MCP_TOKEN")
	assert.Error(t, run(context.Background(), server, "http", "bad-addr", "secret"))
}

func TestRun_WaitsForShutdown(t *testing.T) {
	server := mcp.NewServer(mcp.NewClient("http://127.0.0.1:1", "", ""), mcp.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, server, "http", "127.0.0.1:0", "") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after shutdown")
	}
}
