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

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery/query"
)

func TestRewrite_ScoringClauses(t *testing.T) {
	nested := m{"nested": m{"path": "comments", "query": matchQuery("comments.name", "john")}}
	wantIDs := m{"terms": m{"_id": []string{"1"}}}
	tests := []struct {
		name  string
		query m
		want  m
	}{
		{
			name:  "script_score query",
			query: m{"script_score": m{"query": nested, "script": "_score"}},
			want:  m{"script_score": m{"query": wantIDs, "script": "_score"}},
		},
		{
			name:  "pinned organic",
			query: m{"pinned": m{"ids": []interface{}{"2"}, "organic": nested}},
			want:  m{"pinned": m{"ids": []interface{}{"2"}, "organic": wantIDs}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []execCall
			got, err := Rewrite(tt.query, fakeExec(&calls, "1"))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Len(t, calls, 1)
		})
	}
}

func TestTransform_ScalarSubQuery(t *testing.T) {
	fns := map[string]TransformFunc{"rule": func(map[string]interface{}) (map[string]interface{}, error) {
		return m{"match_none": m{}}, nil
	}}
	got, err := Transform(m{"bool": m{"must": "x"}}, fns)
	require.NoError(t, err)
	assert.Equal(t, m{"bool": m{"must": "x"}}, got)
}

func TestValueMatches_More(t *testing.T) {
	tests := []struct {
		name        string
		typ         string
		input, want interface{}
		match       bool
	}{
		{"unknown type", "regex", "pugs", "pugs", false},
		{"fuzzy short term one edit", "fuzzy", "pigs", "pugs", true},
		{"fuzzy short term two edits", "fuzzy", "pins", "pugs", false},
		{"exact number as string", "exact", 1.0, "1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.match, valueMatches(tt.typ, tt.input, tt.want))
		})
	}
}

func TestAutoFuzziness(t *testing.T) {
	tests := []struct {
		term string
		want int
	}{
		{"ab", 0}, {"abc", 1}, {"abcde", 1}, {"abcdef", 2},
	}
	for _, tt := range tests {
		t.Run(tt.term, func(t *testing.T) {
			assert.Equal(t, tt.want, autoFuzziness(tt.term))
		})
	}
}

func TestRuleQuery_BoostAndPinnedLimit(t *testing.T) {
	ids := make([]string, query.MaxPinnedDocs+5)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
	}
	ruleset := &meta.QueryRuleset{ID: "rs", Rules: []*meta.QueryRule{
		{ID: "pin", Type: "pinned", Criteria: []meta.QueryRuleCriteria{{Type: "always"}}, Actions: meta.QueryRuleActions{IDs: ids}},
	}}
	lookup := func(string) (*meta.QueryRuleset, error) { return ruleset, nil }
	organic := m{"match_all": m{}}

	got, err := RuleQuery(m{"organic": organic, "ruleset_ids": "rs", "match_criteria": m{}, "boost": 2.0, "_name": "n"}, lookup)
	require.NoError(t, err)
	pinned := got["pinned"].(m)
	assert.Len(t, pinned["ids"], query.MaxPinnedDocs)
	assert.Equal(t, "0", pinned["ids"].([]interface{})[0])
	assert.Equal(t, organic, pinned["organic"])
}
