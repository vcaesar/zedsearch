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

package uquery

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestRewrite_Passthrough(t *testing.T) {
	nested := map[string]interface{}{"nested": map[string]interface{}{"path": "comments", "query": matchQuery("comments.name", "john")}}
	wantIDs := map[string]interface{}{"terms": map[string]interface{}{"_id": []string{"1"}}}
	var nilQuery *meta.Query

	tests := []struct {
		name  string
		query interface{}
		want  interface{}
	}{
		{"unsupported type", "x", "x"},
		{"nil meta query", nilQuery, nilQuery},
		{"bool not object", map[string]interface{}{"bool": "x"}, map[string]interface{}{"bool": "x"}},
		{"bool scalar clause", map[string]interface{}{"bool": map[string]interface{}{"must": "x"}}, map[string]interface{}{"bool": map[string]interface{}{"must": "x"}}},
		{
			"bool array with scalar item",
			map[string]interface{}{"bool": map[string]interface{}{"should": []interface{}{"x", nested}}},
			map[string]interface{}{"bool": map[string]interface{}{"should": []interface{}{"x", wantIDs}}},
		},
		{"function_score not object", map[string]interface{}{"function_score": "x"}, map[string]interface{}{"function_score": "x"}},
		{
			"function_score without functions",
			map[string]interface{}{"function_score": map[string]interface{}{"query": nested}},
			map[string]interface{}{"function_score": map[string]interface{}{"query": wantIDs}},
		},
		{
			"function_score scalar function",
			map[string]interface{}{"function_score": map[string]interface{}{"functions": []interface{}{"x"}}},
			map[string]interface{}{"function_score": map[string]interface{}{"functions": []interface{}{"x"}}},
		},
		{
			"parent_id accepted options",
			map[string]interface{}{"parent_id": map[string]interface{}{"type": "answer", "id": "Q1", "boost": 1.0, "ignore_unmapped": true}},
			map[string]interface{}{"bool": map[string]interface{}{"filter": []interface{}{
				map[string]interface{}{"term": map[string]interface{}{meta.JoinNameField: "answer"}},
				map[string]interface{}{"term": map[string]interface{}{meta.JoinParentField: "Q1"}},
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Rewrite(tt.query, fakeExec(&[]execCall{}, "1"))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRewrite_EmptyValuesSkipped(t *testing.T) {
	tests := []struct {
		name  string
		query map[string]interface{}
		want  map[string]interface{}
	}{
		{
			"nested",
			map[string]interface{}{"nested": map[string]interface{}{"path": "comments", "query": matchQuery("comments.name", "john")}},
			map[string]interface{}{"terms": map[string]interface{}{"_id": []string{"1"}}},
		},
		{
			"has_child",
			map[string]interface{}{"has_child": map[string]interface{}{"type": "answer", "query": matchQuery("body", "go")}},
			map[string]interface{}{"terms": map[string]interface{}{"_id": []string{"1"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Rewrite(tt.query, fakeExec(&[]execCall{}, "", "1", "1"))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRewrite_ErrorPaths(t *testing.T) {
	bad := map[string]interface{}{"nested": map[string]interface{}{"path": "comments"}}
	const badErr = "[nested] requires [query] field"
	match := matchQuery("a", "b")

	tests := []struct {
		name  string
		query interface{}
		want  string
	}{
		{"meta query not marshalable", &meta.Query{Nested: &meta.NestedQuery{Path: "p", Query: make(chan int)}}, "query must be map"},
		{"bool clause", map[string]interface{}{"bool": map[string]interface{}{"must": bad}}, badErr},
		{"bool array clause", map[string]interface{}{"bool": map[string]interface{}{"filter": []interface{}{bad}}}, badErr},
		{"function_score query", map[string]interface{}{"function_score": map[string]interface{}{"query": bad}}, badErr},
		{"function_score filter", map[string]interface{}{"function_score": map[string]interface{}{"functions": []interface{}{map[string]interface{}{"filter": bad}}}}, badErr},
		{"has_child not object", map[string]interface{}{"has_child": "x"}, "[has_child] query doesn't support value type string"},
		{"has_child bad min_children", map[string]interface{}{"has_child": map[string]interface{}{"type": "a", "query": match, "min_children": "x"}}, "min_children doesn't support"},
		{"has_child bad max_children", map[string]interface{}{"has_child": map[string]interface{}{"type": "a", "query": match, "max_children": []interface{}{}}}, "max_children doesn't support"},
		{"has_child unknown field", map[string]interface{}{"has_child": map[string]interface{}{"type": "a", "query": match, "nope": 1}}, "[has_child] unknown field [nope]"},
		{"has_child without query", map[string]interface{}{"has_child": map[string]interface{}{"type": "a"}}, "[has_child] requires [query] field"},
		{"has_child bad sub query", map[string]interface{}{"has_child": map[string]interface{}{"type": "a", "query": bad}}, badErr},
		{"has_parent not object", map[string]interface{}{"has_parent": "x"}, "[has_parent] query doesn't support value type string"},
		{"has_parent unknown field", map[string]interface{}{"has_parent": map[string]interface{}{"type": "q", "query": match, "nope": 1}}, "[has_parent] unknown field [nope]"},
		{"has_parent without type", map[string]interface{}{"has_parent": map[string]interface{}{"query": match}}, "[has_parent] requires [parent_type] field"},
		{"has_parent bad sub query", map[string]interface{}{"has_parent": map[string]interface{}{"parent_type": "q", "query": bad}}, badErr},
		{"parent_id not object", map[string]interface{}{"parent_id": "x"}, "[parent_id] query doesn't support value type string"},
		{"parent_id unknown field", map[string]interface{}{"parent_id": map[string]interface{}{"type": "a", "id": "1", "nope": 1}}, "[parent_id] unknown field [nope]"},
		{"parent_id without type", map[string]interface{}{"parent_id": map[string]interface{}{"id": "1"}}, "[parent_id] requires [type] field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Rewrite(tt.query, fakeExec(&[]execCall{}, "1"))
			assert.Nil(t, got)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestRewrite_JoinExecError(t *testing.T) {
	failing := func(_ map[string]interface{}, _ string) ([]string, error) {
		return nil, fmt.Errorf("reader closed")
	}
	for name, query := range map[string]map[string]interface{}{
		"has_child":  {"has_child": map[string]interface{}{"type": "answer", "query": matchQuery("a", "b")}},
		"has_parent": {"has_parent": map[string]interface{}{"parent_type": "question", "query": matchQuery("a", "b")}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Rewrite(query, failing)
			assert.ErrorContains(t, err, "reader closed")
		})
	}
}

func TestRewriteLimit(t *testing.T) {
	max := config.Global.MaxResults
	t.Cleanup(func() { config.Global.MaxResults = max })

	tests := []struct {
		name       string
		maxResults int
		want       int
	}{
		{"unset", 0, MaxRewriteMatches},
		{"negative", -1, MaxRewriteMatches},
		{"below hard limit", 10, 10},
		{"above hard limit", MaxRewriteMatches + 1, MaxRewriteMatches},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.Global.MaxResults = tt.maxResults
			assert.Equal(t, tt.want, RewriteLimit())
		})
	}
}
