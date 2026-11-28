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

package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorded struct {
	method, uri, body, user, pass string
}

// fakeZed records the last request and answers with status and body.
func fakeZed(t *testing.T, status int, body string) (*Client, *recorded) {
	t.Helper()
	last := new(recorded)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		last.method, last.uri, last.body = r.Method, r.URL.RequestURI(), string(data)
		last.user, last.pass, _ = r.BasicAuth()
		w.WriteHeader(status)
		_, err = w.Write([]byte(body))
		assert.NoError(t, err)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/", "admin", "secret"), last
}

func connect(t *testing.T, c *Client, opts Options) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := NewServer(c, opts).Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, ss.Close()) })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cs.Close()) })
	return cs
}

func toolNames(t *testing.T, cs *sdk.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func TestNewServer_Tools(t *testing.T) {
	c, _ := fakeZed(t, http.StatusOK, "{}")
	readTools := []string{"cluster_health", "count", "get_document", "get_mappings", "list_indices", "search", "sql"}

	assert.Equal(t, readTools, toolNames(t, connect(t, c, Options{})))

	all := append([]string{"delete_document", "index_document"}, readTools...)
	sort.Strings(all)
	assert.Equal(t, all, toolNames(t, connect(t, c, Options{AllowWrite: true})))
}

func TestNewServer_CallTool(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		args   map[string]any
		method string
		uri    string
		body   string
	}{
		{"list all indices", "list_indices", map[string]any{}, http.MethodGet, "/es/_cat/indices?format=json", ""},
		{"list pattern", "list_indices", map[string]any{"pattern": "logs-*"}, http.MethodGet, "/es/_cat/indices/logs-%2A?format=json", ""},
		{"mappings", "get_mappings", map[string]any{"index": "books"}, http.MethodGet, "/es/books/_mapping", ""},
		{"search", "search", map[string]any{"index": "books", "query_body": map[string]any{"size": 1}}, http.MethodPost, "/es/books/_search", `{"size":1}`},
		{"count without query", "count", map[string]any{"index": "books"}, http.MethodPost, "/es/books/_count", `{}`},
		{"sql", "sql", map[string]any{"query": "SELECT * FROM books"}, http.MethodPost, "/es/_sql?format=json", `{"query":"SELECT * FROM books"}`},
		{"get document", "get_document", map[string]any{"index": "books", "id": "a/1"}, http.MethodGet, "/es/books/_doc/a%2F1", ""},
		{"health", "cluster_health", map[string]any{}, http.MethodGet, "/es/_cluster/health", ""},
		{"index with id", "index_document", map[string]any{"index": "books", "id": "1", "document": map[string]any{"t": "go"}}, http.MethodPut, "/es/books/_doc/1", `{"t":"go"}`},
		{"index without id", "index_document", map[string]any{"index": "books", "document": map[string]any{"t": "go"}}, http.MethodPost, "/es/books/_doc", `{"t":"go"}`},
		{"delete document", "delete_document", map[string]any{"index": "books", "id": "1"}, http.MethodDelete, "/es/books/_doc/1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, last := fakeZed(t, http.StatusOK, `{"ok":true}`)
			cs := connect(t, c, Options{AllowWrite: true})

			res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tt.tool, Arguments: tt.args})
			require.NoError(t, err)
			assert.False(t, res.IsError)
			require.Len(t, res.Content, 1)
			assert.Equal(t, `{"ok":true}`, res.Content[0].(*sdk.TextContent).Text)
			assert.Equal(t, recorded{tt.method, tt.uri, tt.body, "admin", "secret"}, *last)
		})
	}
}

func TestNewServer_CallToolErrors(t *testing.T) {
	c, _ := fakeZed(t, http.StatusNotFound, `{"error":"index not found"}`)
	cs := connect(t, c, Options{})

	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_mappings", Arguments: map[string]any{"index": "missing"}})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*sdk.TextContent).Text, "index not found")

	res, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "search", Arguments: map[string]any{}})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*sdk.TextContent).Text, `missing properties: ["index" "query_body"]`)

	_, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "delete_document", Arguments: map[string]any{"index": "a", "id": "1"}})
	assert.Error(t, err, "write tools are disabled by default")
}

func TestNewServer_InvalidArgs(t *testing.T) {
	c, last := fakeZed(t, http.StatusOK, "{}")
	cs := connect(t, c, Options{AllowWrite: true})
	tests := []struct {
		name string
		tool string
		args map[string]any
		err  string
	}{
		{"empty index", "get_mappings", map[string]any{"index": ""}, `got ""`},
		{"dot dot index", "search", map[string]any{"index": "..", "query_body": map[string]any{}}, `got ".."`},
		{"dot pattern", "list_indices", map[string]any{"pattern": "."}, `got "."`},
		{"dot dot id", "get_document", map[string]any{"index": "books", "id": ".."}, `got ".."`},
		{"empty delete id", "delete_document", map[string]any{"index": "books", "id": ""}, `got ""`},
		{"empty count index", "count", map[string]any{"index": ""}, `got ""`},
		{"empty index_document index", "index_document", map[string]any{"index": "", "document": map[string]any{}}, `got ""`},
		{"empty sql", "sql", map[string]any{"query": ""}, "query must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tt.tool, Arguments: tt.args})
			require.NoError(t, err)
			assert.True(t, res.IsError)
			assert.Contains(t, res.Content[0].(*sdk.TextContent).Text, tt.err)
		})
	}
	assert.Empty(t, last.method, "invalid calls never reach ZedSearch")
}

func TestNewServer_Annotations(t *testing.T) {
	c, _ := fakeZed(t, http.StatusOK, "{}")
	res, err := connect(t, c, Options{AllowWrite: true}).ListTools(context.Background(), nil)
	require.NoError(t, err)
	for _, tool := range res.Tools {
		switch tool.Name {
		case "index_document":
			assert.Nil(t, tool.Annotations, "POST without id is not idempotent")
		case "delete_document":
			assert.True(t, *tool.Annotations.DestructiveHint)
			assert.True(t, tool.Annotations.IdempotentHint)
		default:
			assert.True(t, tool.Annotations.ReadOnlyHint, tool.Name)
		}
	}
}

func TestClient_Do(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "", "")
	_, err := c.Do(context.Background(), http.MethodGet, "/es/", nil)
	assert.Error(t, err)

	_, err = c.Do(context.Background(), http.MethodPost, "/es/", map[string]any{"bad": make(chan int)})
	assert.Error(t, err)

	_, err = c.Do(context.Background(), "BAD METHOD", "/es/", nil)
	assert.Error(t, err)
}

func TestClient_CleartextAuth(t *testing.T) {
	tests := []struct {
		url, user string
		want      bool
	}{
		{"http://localhost:4080", "admin", false},
		{"http://127.0.0.1:4080", "admin", false},
		{"http://[::1]:4080", "admin", false},
		{"https://zed.example.com", "admin", false},
		{"http://zed.example.com", "", false},
		{"http://zed.example.com", "admin", true},
		{"http://10.0.0.5:4080", "admin", true},
		{"http://admin:secret@zed.example.com", "", true},
		{"https://admin:secret@zed.example.com", "", false},
		{"://bad", "admin", false},
	}
	for _, tt := range tests {
		t.Run(tt.url+"/"+tt.user, func(t *testing.T) {
			assert.Equal(t, tt.want, NewClient(tt.url, tt.user, "secret").CleartextAuth())
		})
	}
}

func TestClient_Endpoint(t *testing.T) {
	tests := map[string]string{
		"http://admin:secret@zed.example.com:4080/es?x=1": "http://zed.example.com:4080",
		"https://zed.example.com/":                        "https://zed.example.com",
		"://bad":                                          "invalid URL",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			got := NewClient(in, "", "").Endpoint()
			assert.Equal(t, want, got)
			assert.NotContains(t, got, "secret")
		})
	}
}

func TestCheckRedirect(t *testing.T) {
	tests := []struct {
		target string
		auth   bool
	}{
		{"https://zed.example.com/es/", true},
		{"http://localhost:4080/es/", true},
		{"http://zed.example.com/es/", false},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			req.SetBasicAuth("admin", "secret")
			require.NoError(t, checkRedirect(req, make([]*http.Request, 1)))
			_, _, ok := req.BasicAuth()
			assert.Equal(t, tt.auth, ok)
		})
	}
	req := httptest.NewRequest(http.MethodGet, "https://zed.example.com/", nil)
	assert.ErrorContains(t, checkRedirect(req, make([]*http.Request, 10)), "redirects")
}

func TestClient_RedirectKeepsLoopbackAuth(t *testing.T) {
	var user string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusFound)
			return
		}
		user, _, _ = r.BasicAuth()
	}))
	t.Cleanup(srv.Close)
	_, err := NewClient(srv.URL, "admin", "secret").Do(context.Background(), http.MethodGet, "/old", nil)
	require.NoError(t, err)
	assert.Equal(t, "admin", user)
}
