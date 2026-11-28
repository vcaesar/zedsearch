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
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		cfg  config
		err  string
	}{
		{"unknown transport", context.Background(), config{transport: "grpc"}, "unknown transport"},
		{"loopback shuts down with ctx", canceled, config{transport: "http", addr: "127.0.0.1:0"}, ""},
		{"non-loopback with a token", canceled, config{transport: "http", addr: ":0", token: "secret"}, ""},
		{"non-loopback without a token", context.Background(), config{transport: "http", addr: "0.0.0.0:0"}, "ZED_MCP_TOKEN"},
		{"cert without key", context.Background(), config{transport: "http", addr: "127.0.0.1:0", certFile: "cert.pem"}, "together"},
		{"missing tls files", context.Background(), config{transport: "http", addr: "127.0.0.1:0", certFile: "missing.pem", keyFile: "missing.pem"}, "missing.pem"},
		{"bad addr", context.Background(), config{transport: "http", addr: "bad-addr", token: "secret"}, "bad-addr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.ctx, server, tt.cfg)
			if tt.err == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.err)
			}
		})
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// run must drain a tool call that is in flight when the context is canceled.
func TestRun_DrainsInFlightCall(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	var arrivedOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrivedOnce.Do(func() { close(arrived) })
		<-release
		_, err := io.WriteString(w, `{"status":"green"}`)
		assert.NoError(t, err)
	}))
	defer backend.Close()
	defer unblock() // runs first, so Close never waits on a blocked handler

	addr := freeAddr(t)
	server := mcp.NewServer(mcp.NewClient(backend.URL, "", ""), mcp.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, server, config{transport: "http", addr: addr}) }()

	transport := &sdk.StreamableClientTransport{Endpoint: "http://" + addr + "/", DisableStandaloneSSE: true}
	var cs *sdk.ClientSession
	require.Eventually(t, func() bool {
		var err error
		cs, err = sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(context.Background(), transport, nil)
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)

	callc := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "cluster_health", Arguments: map[string]any{}})
		if err == nil && res.IsError {
			err = errors.New("tool returned an error")
		}
		callc <- err
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("tool call did not reach the backend")
	}

	cancel()
	select {
	case err := <-errc:
		t.Fatalf("run returned while a call was in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	unblock()
	require.NoError(t, <-callc, "in-flight call completes during shutdown")
	select {
	case err := <-errc:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the drain")
	}
	if err := cs.Close(); err != nil {
		t.Logf("close session after shutdown: %v", err)
	}
}

// A client without the token that withholds its declared body must get its
// 401 and lose the connection instead of pinning it.
func TestRequireToken_WithheldBody(t *testing.T) {
	srv := httptest.NewServer(testServer("secret"))
	t.Cleanup(srv.Close)
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })
	_, err = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: 1\r\n\r\n")
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))

	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	require.NoError(t, err, "401 must not wait for the withheld body")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	_, err = r.ReadByte()
	assert.ErrorIs(t, err, io.EOF, "server closes the connection")
}
