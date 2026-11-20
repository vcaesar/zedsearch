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

package uquery

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

type m = map[string]interface{}

func intPtr(i int) *int { return &i }

func TestValidateQueryRule(t *testing.T) {
	valid := func() *meta.QueryRule {
		return &meta.QueryRule{ID: "r", Type: "pinned",
			Criteria: []meta.QueryRuleCriteria{{Type: "exact", Metadata: "q", Values: []interface{}{"a"}}},
			Actions:  meta.QueryRuleActions{IDs: []string{"1"}}}
	}
	many := make([]string, 101)
	tests := []struct {
		name   string
		modify func(r *meta.QueryRule)
		want   string
	}{
		{"valid", func(r *meta.QueryRule) {}, ""},
		{"always", func(r *meta.QueryRule) { r.Criteria = []meta.QueryRuleCriteria{{Type: "always"}} }, ""},
		{"no id", func(r *meta.QueryRule) { r.ID = "" }, "requires [rule_id]"},
		{"bad type", func(r *meta.QueryRule) { r.Type = "boost" }, "unknown type"},
		{"no criteria", func(r *meta.QueryRule) { r.Criteria = nil }, "requires [criteria]"},
		{"bad criteria", func(r *meta.QueryRule) { r.Criteria[0].Type = "regex" }, "unknown criteria type"},
		{"no values", func(r *meta.QueryRule) { r.Criteria[0].Values = nil }, "requires [metadata] and [values]"},
		{"numeric values", func(r *meta.QueryRule) { r.Criteria[0].Type = "gt" }, "must be numbers"},
		{"ids and docs", func(r *meta.QueryRule) { r.Actions.Docs = []meta.QueryRuleDocRef{{ID: "2"}} }, "one of [ids] or [docs]"},
		{"no actions", func(r *meta.QueryRule) { r.Actions.IDs = nil }, "requires [actions]"},
		{"too many", func(r *meta.QueryRule) { r.Actions.IDs = many }, "more than 100"},
		{"negative priority", func(r *meta.QueryRule) { r.Priority = intPtr(-1) }, "priority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			tt.modify(r)
			err := ValidateQueryRule(r)
			if tt.want == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.want)
			}
		})
	}
	t.Run("nil", func(t *testing.T) {
		require.NotPanics(t, func() {
			assert.ErrorContains(t, ValidateQueryRule(nil), "[query_rule] must be an object")
		})
	})
}

func TestMatchQueryRules(t *testing.T) {
	crit := func(typ string, values ...interface{}) meta.QueryRuleCriteria {
		return meta.QueryRuleCriteria{Type: typ, Metadata: "q", Values: values}
	}
	tests := []struct {
		criteria meta.QueryRuleCriteria
		input    interface{}
		want     bool
	}{
		{crit("exact", "pugs"), "pugs", true},
		{crit("exact", "pugs"), "pug", false},
		{crit("prefix", "pu"), "pugs", true},
		{crit("suffix", "gs"), "pugs", true},
		{crit("contains", "ug"), "pugs", true},
		{crit("fuzzy", "puggles"), "Pugglez", true},
		{crit("fuzzy", "ab"), "ac", false},
		{crit("lt", 10.0), 5.0, true},
		{crit("lte", 10.0), 10.0, true},
		{crit("gt", 10.0), 10.0, false},
		{crit("gte", 10.0), "11", true},
		{crit("gt", 10.0), "x", false},
		{crit("exact", "a", "b"), "b", true},
		{meta.QueryRuleCriteria{Type: "always"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %v", tt.criteria.Type, tt.input), func(t *testing.T) {
			rs := &meta.QueryRuleset{Rules: []*meta.QueryRule{{ID: "r", Criteria: []meta.QueryRuleCriteria{tt.criteria}}}}
			input := m{}
			if tt.input != nil {
				input["q"] = tt.input
			}
			assert.Equal(t, tt.want, len(MatchQueryRules(rs, input)) == 1)
		})
	}
	rs := &meta.QueryRuleset{Rules: []*meta.QueryRule{{ID: "r", Criteria: []meta.QueryRuleCriteria{crit("exact", "a")}}}}
	assert.Empty(t, MatchQueryRules(rs, m{"other": "a"}))
}

func TestSortQueryRules(t *testing.T) {
	rules := []*meta.QueryRule{{ID: "none"}, {ID: "p5", Priority: intPtr(5)}, {ID: "p1", Priority: intPtr(1)}, {ID: "none2"}}
	SortQueryRules(rules)
	ids := []string{}
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"p1", "p5", "none", "none2"}, ids)
}

func TestRuleQuery(t *testing.T) {
	always := []meta.QueryRuleCriteria{{Type: "always"}}
	rulesets := map[string]*meta.QueryRuleset{
		"rs": {ID: "rs", Rules: []*meta.QueryRule{
			{ID: "pin", Type: "pinned", Criteria: []meta.QueryRuleCriteria{{Type: "exact", Metadata: "q", Values: []interface{}{"pugs"}}},
				Actions: meta.QueryRuleActions{IDs: []string{"1", "2"}}},
			{ID: "drop", Type: "exclude", Criteria: always, Actions: meta.QueryRuleActions{Docs: []meta.QueryRuleDocRef{{Index: "i", ID: "2"}}}},
		}},
	}
	lookup := func(id string) (*meta.QueryRuleset, error) {
		if rs, ok := rulesets[id]; ok {
			return rs, nil
		}
		return nil, fmt.Errorf("not found")
	}
	organic := m{"match_all": m{}}

	got, err := RuleQuery(m{"organic": organic, "ruleset_ids": []interface{}{"rs"}, "match_criteria": m{"q": "pugs"}}, lookup)
	require.NoError(t, err)
	// i/2 is excluded, 2 stays pinned in the other indexes
	dropped := []interface{}{m{"bool": m{"filter": []interface{}{
		m{"term": m{"_index": "i"}}, m{"ids": m{"values": []interface{}{"2"}}},
	}}}}
	assert.Equal(t, m{"bool": m{
		"must":     m{"pinned": m{"ids": []interface{}{"1", "2"}, "organic": organic}},
		"must_not": dropped,
	}}, got)

	got, err = RuleQuery(m{"organic": organic, "ruleset_id": "rs", "match_criteria": m{"q": "cats"}}, lookup)
	require.NoError(t, err)
	assert.Equal(t, m{"bool": m{"must": organic, "must_not": dropped}}, got)

	tests := []struct {
		name string
		body m
		want string
	}{
		{"no organic", m{"ruleset_ids": []interface{}{"rs"}, "match_criteria": m{}}, "requires [organic]"},
		{"no rulesets", m{"organic": organic, "match_criteria": m{}}, "requires [ruleset_ids]"},
		{"no criteria", m{"organic": organic, "ruleset_ids": []interface{}{"rs"}}, "requires [match_criteria]"},
		{"bad criteria", m{"organic": organic, "ruleset_ids": []interface{}{"rs"}, "match_criteria": "x"}, "doesn't support"},
		{"bad ids", m{"organic": organic, "ruleset_ids": 1, "match_criteria": m{}}, "doesn't support"},
		{"bad id", m{"organic": organic, "ruleset_ids": []interface{}{m{}}, "match_criteria": m{}}, "doesn't support"},
		{"missing ruleset", m{"organic": organic, "ruleset_ids": []interface{}{"x"}, "match_criteria": m{}}, "not found"},
		{"unknown", m{"organic": organic, "x": 1}, "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RuleQuery(tt.body, lookup)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestRuleResult(t *testing.T) {
	organic := m{"match_all": m{}}
	ids := func(values ...interface{}) m { return m{"ids": m{"values": values}} }
	inIndex := func(index string, values ...interface{}) m {
		return m{"bool": m{"filter": []interface{}{m{"term": m{"_index": index}}, ids(values...)}}}
	}
	tests := []struct {
		name             string
		organic          interface{}
		pinned, excluded []ruleDoc
		want             m
	}{
		{name: "organic that is not an object", organic: "x", want: m{"bool": m{"must": "x"}}},
		{name: "organic only", organic: organic, want: organic},
		{
			name:    "duplicate and empty ids",
			organic: organic,
			pinned:  []ruleDoc{{id: "1"}, {id: "1"}, {id: ""}, {id: "2"}},
			want:    m{"pinned": m{"ids": []interface{}{"1", "2"}, "organic": organic}},
		},
		{
			name:    "docs keep their index",
			organic: organic,
			pinned:  []ruleDoc{{index: "a", id: "1"}, {index: "b", id: "1"}, {index: "a", id: "1"}, {id: "2"}},
			want: m{"pinned": m{"organic": organic, "docs": []interface{}{
				m{"_index": "a", "_id": "1"}, m{"_index": "b", "_id": "1"}, m{"_id": "2"},
			}}},
		},
		{
			name:    "pinned in any index hides the same id of one index",
			organic: organic,
			pinned:  []ruleDoc{{id: "1"}, {index: "a", id: "1"}},
			want:    m{"pinned": m{"ids": []interface{}{"1"}, "organic": organic}},
		},
		{
			name:     "excluded in any index",
			organic:  organic,
			pinned:   []ruleDoc{{id: "1"}, {index: "a", id: "2"}, {id: "3"}},
			excluded: []ruleDoc{{id: "1"}, {id: "2"}},
			want: m{"bool": m{
				"must":     m{"pinned": m{"ids": []interface{}{"3"}, "organic": organic}},
				"must_not": []interface{}{ids("1", "2")},
			}},
		},
		{
			name:     "excluded in one index",
			organic:  organic,
			pinned:   []ruleDoc{{index: "a", id: "1"}, {index: "b", id: "1"}},
			excluded: []ruleDoc{{index: "a", id: "1"}, {index: "a", id: "2"}, {id: "3"}, {index: "b", id: "4"}},
			want: m{"bool": m{
				"must":     m{"pinned": m{"docs": []interface{}{m{"_index": "b", "_id": "1"}}, "organic": organic}},
				"must_not": []interface{}{inIndex("a", "1", "2"), ids("3"), inIndex("b", "4")},
			}},
		},
		{
			name:     "all pinned excluded",
			organic:  organic,
			pinned:   []ruleDoc{{id: "1"}},
			excluded: []ruleDoc{{id: "1"}},
			want:     m{"bool": m{"must": organic, "must_not": []interface{}{ids("1")}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ruleResult(tt.organic, tt.pinned, tt.excluded))
		})
	}
}

func TestRescore(t *testing.T) {
	rs, err := ParseRescore(m{"window_size": 5.0, "query": m{
		"rescore_query": m{"match_all": m{}}, "query_weight": 0.5, "rescore_query_weight": 2.0, "score_mode": "Multiply",
	}})
	require.NoError(t, err)
	require.Len(t, rs, 1)
	assert.Equal(t, 5, rs[0].WindowSize)
	assert.Equal(t, "multiply", rs[0].ScoreMode)
	assert.Equal(t, 20, RescoreWindow(rs, 10, 10))
	assert.Equal(t, 5, RescoreWindow(rs, 0, 2))

	list, err := ParseRescore([]interface{}{m{"query": m{"rescore_query": m{"match_all": m{}}}}})
	require.NoError(t, err)
	assert.Equal(t, DefaultRescoreWindow, list[0].WindowSize)
	none, err := ParseRescore(nil)
	assert.NoError(t, err)
	assert.Nil(t, none)

	combine := []struct {
		mode    string
		matched bool
		want    float64
	}{
		{"total", true, 1 + 6}, {"multiply", true, 6}, {"avg", true, 3.5}, {"max", true, 6}, {"min", true, 1}, {"total", false, 1},
	}
	for _, tt := range combine {
		t.Run(tt.mode, func(t *testing.T) {
			r := &Rescorer{QueryWeight: 0.5, RescoreQueryWeight: 2, ScoreMode: tt.mode}
			assert.Equal(t, tt.want, r.Combine(2, 3, tt.matched))
		})
	}

	errs := []struct {
		name string
		v    interface{}
		want string
	}{
		{"not object", "x", "must be an object"},
		{"bad window", m{"window_size": -1.0}, "window_size"},
		{"bad query", m{"query": "x"}, "doesn't support"},
		{"unknown rescorer", m{"learning_to_rank": m{}}, "unknown rescorer"},
		{"no rescore_query", m{"query": m{}}, "requires [query]"},
		{"bad rescore_query", m{"query": m{"rescore_query": "x"}}, "doesn't support"},
		{"bad weight", m{"query": m{"rescore_query": m{}, "query_weight": "x"}}, "doesn't support"},
		{"bad mode", m{"query": m{"rescore_query": m{}, "score_mode": "first"}}, "unknown score_mode"},
		{"bad mode type", m{"query": m{"rescore_query": m{}, "score_mode": m{}}}, "unknown score_mode"},
		{"unknown field", m{"query": m{"rescore_query": m{}, "x": 1}}, "unknown field"},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRescore(tt.v)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestTransform(t *testing.T) {
	fns := map[string]TransformFunc{
		"rule": func(body map[string]interface{}) (map[string]interface{}, error) {
			if body["fail"] != nil {
				return nil, fmt.Errorf("boom")
			}
			return m{"ids": m{"values": body["id"]}}, nil
		},
	}
	rule := m{"rule": m{"id": "1"}}
	want := m{"ids": m{"values": "1"}}
	query := m{"bool": m{
		"must":   []interface{}{rule, "x"},
		"filter": m{"constant_score": m{"filter": rule}},
		"should": m{"function_score": m{"query": rule, "functions": []interface{}{m{"filter": rule, "weight": 1.0}, "x", m{"weight": 2.0}}}},
	}}
	got, err := Transform(query, fns)
	require.NoError(t, err)
	b := got.(m)["bool"].(m)
	assert.Equal(t, []interface{}{want, "x"}, b["must"])
	assert.Equal(t, want, b["filter"].(m)["constant_score"].(m)["filter"])
	fs := b["should"].(m)["function_score"].(m)
	assert.Equal(t, want, fs["query"])
	assert.Equal(t, want, fs["functions"].([]interface{})[0].(m)["filter"])
	// input is not modified
	assert.Equal(t, rule, query["bool"].(m)["must"].([]interface{})[0])

	same, err := Transform(m{"rule": "x"}, fns)
	require.NoError(t, err)
	assert.Equal(t, m{"rule": "x"}, same)
	none, err := Transform(nil, fns)
	assert.NoError(t, err)
	assert.Nil(t, none)

	_, err = Transform(m{"bool": m{"must": []interface{}{m{"rule": m{"fail": true}}}}}, fns)
	assert.ErrorContains(t, err, "boom")
	_, err = Transform(m{"bool": m{"must": m{"rule": m{"fail": true}}}}, fns)
	assert.ErrorContains(t, err, "boom")
	_, err = Transform(m{"function_score": m{"functions": []interface{}{m{"filter": m{"rule": m{"fail": true}}}}}}, fns)
	assert.ErrorContains(t, err, "boom")

	typed, err := Transform(&meta.Query{MatchAll: &meta.MatchAllQuery{}}, fns)
	require.NoError(t, err)
	assert.Contains(t, typed, "match_all")
}
