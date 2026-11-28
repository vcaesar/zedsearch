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
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

// Package mcp exposes ZedSearch to AI assistants as a Model Context Protocol
// server, modeled on the Elasticsearch and OpenSearch MCP servers. Tools call
// the /es API of a ZedSearch server; write tools are opt-in.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the zedmcp version, set with -ldflags.
var Version = "dev"

// Options configures the MCP server.
type Options struct {
	// AllowWrite registers the index_document and delete_document tools.
	AllowWrite bool
}

type listIndicesArgs struct {
	Pattern string `json:"pattern,omitempty" jsonschema:"index name or pattern such as logs-*, comma separated; all indexes when empty"`
}

type indexArgs struct {
	Index string `json:"index" jsonschema:"index name or pattern"`
}

type searchArgs struct {
	Index     string         `json:"index" jsonschema:"index name or pattern"`
	QueryBody map[string]any `json:"query_body" jsonschema:"Elasticsearch query DSL request body, e.g. {\"query\":{\"match\":{\"title\":\"go\"}},\"size\":10}"`
}

type countArgs struct {
	Index     string         `json:"index" jsonschema:"index name or pattern"`
	QueryBody map[string]any `json:"query_body,omitempty" jsonschema:"optional Elasticsearch query DSL body, e.g. {\"query\":{\"term\":{\"status\":\"ok\"}}}"`
}

type sqlArgs struct {
	Query string `json:"query" jsonschema:"Elasticsearch SQL query, e.g. SELECT * FROM logs LIMIT 10"`
}

type documentArgs struct {
	Index string `json:"index" jsonschema:"index name"`
	ID    string `json:"id" jsonschema:"document id"`
}

type indexDocumentArgs struct {
	Index    string         `json:"index" jsonschema:"index name"`
	ID       string         `json:"id,omitempty" jsonschema:"document id; generated when empty"`
	Document map[string]any `json:"document" jsonschema:"document source"`
}

// NewServer returns an MCP server whose tools are backed by c.
func NewServer(c *Client, opts Options) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "zedsearch", Title: "ZedSearch", Version: Version}, &sdk.ServerOptions{
		Instructions: "ZedSearch is an Elasticsearch-compatible search engine. " +
			"Call list_indices, then get_mappings to learn the fields before search, count or sql.",
	})
	readOnly := &sdk.ToolAnnotations{ReadOnlyHint: true}

	addTool(s, &sdk.Tool{Name: "list_indices", Description: "List indexes with health, document count and store size.", Annotations: readOnly},
		func(ctx context.Context, in listIndicesArgs) ([]byte, error) {
			segments := []string{"_cat", "indices"}
			if in.Pattern != "" {
				segments = append(segments, in.Pattern)
			}
			path, err := esPath(segments...)
			if err != nil {
				return nil, err
			}
			return c.Do(ctx, http.MethodGet, path+"?format=json", nil)
		})
	addTool(s, &sdk.Tool{Name: "get_mappings", Description: "Get the field mappings of an index.", Annotations: readOnly},
		func(ctx context.Context, in indexArgs) ([]byte, error) {
			return c.es(ctx, http.MethodGet, nil, in.Index, "_mapping")
		})
	addTool(s, &sdk.Tool{Name: "search", Description: "Search an index with the Elasticsearch query DSL, including aggregations, sort and highlight.", Annotations: readOnly},
		func(ctx context.Context, in searchArgs) ([]byte, error) {
			return c.es(ctx, http.MethodPost, orEmpty(in.QueryBody), in.Index, "_search")
		})
	addTool(s, &sdk.Tool{Name: "count", Description: "Count the documents of an index matching an optional query.", Annotations: readOnly},
		func(ctx context.Context, in countArgs) ([]byte, error) {
			return c.es(ctx, http.MethodPost, orEmpty(in.QueryBody), in.Index, "_count")
		})
	addTool(s, &sdk.Tool{Name: "sql", Description: "Run an Elasticsearch SQL query; the index is the table name.", Annotations: readOnly},
		func(ctx context.Context, in sqlArgs) ([]byte, error) {
			if in.Query == "" {
				return nil, errors.New("query must not be empty")
			}
			return c.Do(ctx, http.MethodPost, "/es/_sql?format=json", map[string]any{"query": in.Query})
		})
	addTool(s, &sdk.Tool{Name: "get_document", Description: "Get a document by id.", Annotations: readOnly},
		func(ctx context.Context, in documentArgs) ([]byte, error) {
			return c.es(ctx, http.MethodGet, nil, in.Index, "_doc", in.ID)
		})
	addTool(s, &sdk.Tool{Name: "cluster_health", Description: "Get the cluster health.", Annotations: readOnly},
		func(ctx context.Context, _ struct{}) ([]byte, error) {
			return c.Do(ctx, http.MethodGet, "/es/_cluster/health", nil)
		})

	if !opts.AllowWrite {
		return s
	}
	// POST without an id creates a new document on every call, so it is not idempotent.
	addTool(s, &sdk.Tool{Name: "index_document", Description: "Create or replace a document."},
		func(ctx context.Context, in indexDocumentArgs) ([]byte, error) {
			if in.ID == "" {
				return c.es(ctx, http.MethodPost, orEmpty(in.Document), in.Index, "_doc")
			}
			return c.es(ctx, http.MethodPut, orEmpty(in.Document), in.Index, "_doc", in.ID)
		})
	destructive := true
	addTool(s, &sdk.Tool{Name: "delete_document", Description: "Delete a document by id.", Annotations: &sdk.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: true}},
		func(ctx context.Context, in documentArgs) ([]byte, error) {
			return c.es(ctx, http.MethodDelete, nil, in.Index, "_doc", in.ID)
		})
	return s
}

// addTool registers a tool that returns the raw ZedSearch response as text.
func addTool[In any](s *sdk.Server, t *sdk.Tool, call func(context.Context, In) ([]byte, error)) {
	sdk.AddTool(s, t, func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
		body, err := call(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(body)}}}, nil, nil
	})
}

// es sends body to the /es path built from segments.
func (c *Client) es(ctx context.Context, method string, body any, segments ...string) ([]byte, error) {
	path, err := esPath(segments...)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, method, path, body)
}

// esPath escapes segments under /es. Empty and dot segments are rejected
// because they would address a different route.
func esPath(segments ...string) (string, error) {
	var b strings.Builder
	b.WriteString("/es")
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf(`index, pattern and id must not be empty, "." or "..", got %q`, seg)
		}
		b.WriteString("/")
		b.WriteString(url.PathEscape(seg))
	}
	return b.String(), nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
