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

package query

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
)

type obj = map[string]interface{}

// rankScores parses q and returns the score and rank of each document of the
// function score index.
func rankScores(t *testing.T, q obj) (map[string]float64, []string) {
	t.Helper()
	parsed, err := Query(q, testMappings(), nil)
	require.NoError(t, err)

	writer := newFunctionScoreIndex(t)
	defer func() { assert.NoError(t, writer.Close()) }()
	reader, err := writer.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, reader.Close()) }()

	dmi, err := reader.Search(context.Background(), riot.NewTopNSearch(10, parsed))
	require.NoError(t, err)
	scores := map[string]float64{}
	var order []string
	match, err := dmi.Next()
	for err == nil && match != nil {
		var id string
		require.NoError(t, match.VisitStoredFields(func(field string, value []byte) bool {
			if field == "_id" {
				id = string(value)
				return false
			}
			return true
		}))
		scores[id] = match.Score
		order = append(order, id)
		match, err = dmi.Next()
	}
	require.NoError(t, err)
	return scores, order
}

func TestBoostingQuery(t *testing.T) {
	plain, _ := rankScores(t, obj{"match": obj{"name": "apple"}})
	scores, order := rankScores(t, obj{"boosting": obj{
		"positive":       obj{"match": obj{"name": "apple"}},
		"negative":       obj{"term": obj{"name": "red"}},
		"negative_boost": 0.5,
	}})
	assert.Len(t, scores, 2)
	assert.InDelta(t, plain["1"]*0.5, scores["1"], 1e-9)
	assert.InDelta(t, plain["2"], scores["2"], 1e-9)
	assert.Equal(t, "2", order[0])

	scores, _ = rankScores(t, obj{"boosting": obj{
		"positive": obj{"match": obj{"name": "apple"}}, "negative": obj{"match_none": obj{}},
		"negative_boost": 0.1, "boost": 2.0,
	}})
	assert.InDelta(t, plain["1"]*2, scores["1"], 1e-9)
}

func TestBoostingQuery_Errors(t *testing.T) {
	pos, neg := obj{"match_all": obj{}}, obj{"match_none": obj{}}
	tests := []struct {
		name string
		body obj
		want string
	}{
		{"missing positive", obj{"negative": neg, "negative_boost": 0.5}, "requires [positive]"},
		{"missing negative", obj{"positive": pos, "negative_boost": 0.5}, "requires [negative]"},
		{"missing negative_boost", obj{"positive": pos, "negative": neg}, "negative_boost"},
		{"negative_boost too big", obj{"positive": pos, "negative": neg, "negative_boost": 2.0}, "negative_boost"},
		{"bad negative_boost", obj{"positive": pos, "negative": neg, "negative_boost": "x"}, "doesn't support"},
		{"bad positive type", obj{"positive": "x", "negative": neg, "negative_boost": 0.5}, "doesn't support"},
		{"bad positive query", obj{"positive": obj{"nope": obj{}}, "negative": neg, "negative_boost": 0.5}, "failed to parse"},
		{"unknown field", obj{"positive": pos, "negative": neg, "negative_boost": 0.5, "x": 1}, "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BoostingQuery(tt.body, testMappings(), nil)
			assert.ErrorContains(t, err, tt.want)
		})
	}
	q, err := BoostingQuery(obj{"positive": pos, "negative": neg, "negative_boost": 0.5}, testMappings(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1.0, q.(*boostingQuery).Boost())
	assert.NoError(t, q.(*boostingQuery).Validate())
}

func TestPinnedQuery(t *testing.T) {
	scores, order := rankScores(t, obj{"pinned": obj{
		"ids":     []interface{}{"3", "2", "missing"},
		"organic": obj{"match": obj{"name": "apple"}},
	}})
	assert.Equal(t, []string{"3", "2", "1"}, order)
	assert.Greater(t, scores["3"], scores["2"])
	assert.Less(t, scores["1"], 100.0)

	_, order = rankScores(t, obj{"pinned": obj{
		"docs":    []interface{}{obj{"_index": "i", "_id": "1"}},
		"organic": obj{"match": obj{"name": "sky"}},
	}})
	assert.Equal(t, []string{"1", "3"}, order)
}

func TestPinnedQuery_Errors(t *testing.T) {
	organic := obj{"match_all": obj{}}
	many := make([]interface{}, MaxPinnedDocs+1)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	tests := []struct {
		name string
		body obj
		want string
	}{
		{"no ids or docs", obj{"organic": organic}, "one of [ids] or [docs]"},
		{"both ids and docs", obj{"ids": []interface{}{"1"}, "docs": []interface{}{obj{"_id": "1"}}, "organic": organic}, "one of [ids] or [docs]"},
		{"no organic", obj{"ids": []interface{}{"1"}}, "requires [organic]"},
		{"bad ids", obj{"ids": "1", "organic": organic}, "doesn't support"},
		{"bad id", obj{"ids": []interface{}{obj{}}, "organic": organic}, "doesn't support"},
		{"bad docs", obj{"docs": "1", "organic": organic}, "doesn't support"},
		{"bad doc", obj{"docs": []interface{}{"1"}, "organic": organic}, "doesn't support"},
		{"doc without id", obj{"docs": []interface{}{obj{"_index": "i"}}, "organic": organic}, "requires [_id]"},
		{"bad organic", obj{"ids": []interface{}{"1"}, "organic": "x"}, "doesn't support"},
		{"bad organic query", obj{"ids": []interface{}{"1"}, "organic": obj{"nope": obj{}}}, "failed to parse"},
		{"unknown", obj{"ids": []interface{}{"1"}, "organic": organic, "x": 1}, "unknown field"},
		{"too many", obj{"ids": many, "organic": organic}, "cannot pin more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PinnedQuery(tt.body, testMappings(), nil)
			assert.ErrorContains(t, err, tt.want)
		})
	}
	organicQ := riot.NewMatchAllQuery()
	assert.Same(t, organicQ, NewPinnedQuery(nil, organicQ))
}

func TestScriptScoreQuery(t *testing.T) {
	scores, order := rankScores(t, obj{"script_score": obj{
		"query":  obj{"match_all": obj{}},
		"script": obj{"source": "doc['age'].value * params.f + doc['tag'].size()", "params": obj{"f": 2.0}},
	}})
	assert.Equal(t, []string{"3", "2", "1"}, order)
	assert.InDelta(t, 201.0, scores["3"], 1e-9)
	assert.InDelta(t, 3.0, scores["1"], 1e-9)

	scores, _ = rankScores(t, obj{"script_score": obj{
		"query":     obj{"match_all": obj{}},
		"script":    "saturation(doc['age'].value, 10) + Math.log10(10)",
		"min_score": 2.5,
		"boost":     2.0,
	}})
	assert.Len(t, scores, 2)
	assert.InDelta(t, (100.0/110+1)*2, scores["3"], 1e-9)

	scores, _ = rankScores(t, obj{"script_score": obj{
		"query":  obj{"match": obj{"name": "apple"}},
		"script": "_score * 0 + sigmoid(doc['missing'].value + 1, 1, 1)",
	}})
	assert.InDelta(t, 0.5, scores["1"], 1e-9)
}

func TestScriptScoreQuery_Errors(t *testing.T) {
	all := obj{"match_all": obj{}}
	tests := []struct {
		name string
		body obj
		want string
	}{
		{"no query", obj{"script": "1"}, "requires [query]"},
		{"no script", obj{"query": all}, "requires [script]"},
		{"bad script", obj{"query": all, "script": 1}, "script must be"},
		{"compile", obj{"query": all, "script": "1 +"}, "compile err"},
		{"bad query type", obj{"query": "x", "script": "1"}, "doesn't support"},
		{"bad query", obj{"query": obj{"nope": obj{}}, "script": "1"}, "failed to parse"},
		{"bad min_score", obj{"query": all, "script": "1", "min_score": "x"}, "doesn't support"},
		{"unknown", obj{"query": all, "script": "1", "x": 1}, "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ScriptScoreQuery(tt.body, testMappings(), nil)
			assert.ErrorContains(t, err, tt.want)
		})
	}

	for name, script := range map[string]string{"negative": "-1", "string": "'a'", "runtime": "params.x.y"} {
		t.Run("run "+name, func(t *testing.T) {
			q, err := ScriptScoreQuery(obj{"query": all, "script": script}, testMappings(), nil)
			require.NoError(t, err)
			_, err = searchScores(t, q, false)
			assert.Error(t, err)
		})
	}
	q, err := ScriptScoreQuery(obj{"query": all, "script": "1"}, testMappings(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1.0, q.(*scriptScoreQuery).Boost())
	assert.NoError(t, q.(*scriptScoreQuery).Validate())
}

func TestRankFeatureQuery(t *testing.T) {
	tests := []struct {
		name string
		body obj
		want func(v float64) float64
	}{
		{"saturation default", obj{"field": "age"}, func(v float64) float64 { return v / (v + 1) }},
		{"saturation pivot", obj{"field": "age", "saturation": obj{"pivot": 10.0}}, func(v float64) float64 { return v / (v + 10) }},
		{"log", obj{"field": "age", "log": obj{"scaling_factor": 4.0}}, func(v float64) float64 { return math.Log(4 + v) }},
		{"sigmoid", obj{"field": "age", "sigmoid": obj{"pivot": 10.0, "exponent": 0.5}}, func(v float64) float64 {
			return math.Pow(v, 0.5) / (math.Pow(v, 0.5) + math.Pow(10, 0.5))
		}},
		{"linear boost", obj{"field": "age", "linear": obj{}, "boost": 2.0}, func(v float64) float64 { return 2 * v }},
	}
	ages := map[string]float64{"1": 1, "2": 10, "3": 100}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scores, order := rankScores(t, obj{"rank_feature": tt.body})
			assert.Equal(t, []string{"3", "2", "1"}, order)
			for id, age := range ages {
				assert.InDelta(t, tt.want(age), scores[id], 1e-9, id)
			}
		})
	}
}

func TestRankFeatureQuery_Errors(t *testing.T) {
	tests := []struct {
		name string
		body obj
		want string
	}{
		{"no field", obj{}, "requires [field]"},
		{"bad field", obj{"field": obj{}}, "doesn't support"},
		{"bad boost", obj{"field": "a", "boost": "x"}, "doesn't support"},
		{"two functions", obj{"field": "a", "log": obj{"scaling_factor": 1.0}, "linear": obj{}}, "only one"},
		{"bad function", obj{"field": "a", "log": 1}, "doesn't support"},
		{"log without factor", obj{"field": "a", "log": obj{}}, "requires [scaling_factor]"},
		{"sigmoid without pivot", obj{"field": "a", "sigmoid": obj{"exponent": 1.0}}, "requires [pivot]"},
		{"sigmoid without exponent", obj{"field": "a", "sigmoid": obj{"pivot": 1.0}}, "requires [exponent]"},
		{"negative pivot", obj{"field": "a", "saturation": obj{"pivot": -1.0}}, "positive number"},
		{"unknown", obj{"field": "a", "x": 1}, "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RankFeatureQuery(tt.body)
			assert.ErrorContains(t, err, tt.want)
		})
	}
	q, err := RankFeatureQuery(obj{"field": "age"})
	require.NoError(t, err)
	assert.Equal(t, 1.0, q.(*rankFeatureQuery).Boost())
}

func TestMultiMatchFieldBoost(t *testing.T) {
	plain, _ := rankScores(t, obj{"multi_match": obj{"query": "apple", "fields": []interface{}{"name"}}})
	boosted, _ := rankScores(t, obj{"multi_match": obj{"query": "apple", "fields": []interface{}{"name^3"}}})
	assert.InDelta(t, plain["1"]*3, boosted["1"], 1e-9)

	tests := []struct {
		in    string
		field string
		boost float64
	}{
		{"title", "title", 1},
		{"title^2.5", "title", 2.5},
		{"title^x", "title^x", 1},
		{"^2", "^2", 1},
		{"title^-1", "title^-1", 1},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			field, boost := splitFieldBoost(tt.in)
			assert.Equal(t, tt.field, field)
			assert.Equal(t, tt.boost, boost)
		})
	}
}
