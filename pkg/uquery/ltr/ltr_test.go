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

package ltr

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

type m = map[string]interface{}
type l = []interface{}

var nan = math.NaN()

// extractors is the inference_config of two features, title and body, title
// with a default_score.
func extractors() m {
	return m{"learning_to_rank": m{
		"default_params": m{"field": "body"},
		"feature_extractors": l{
			m{"query_extractor": m{"feature_name": "title", "query": m{"match": m{"title": "{{query}}"}}, "default_score": 0.0}},
			m{"query_extractor": m{"feature_name": "body", "query": m{"match": m{"{{field}}": "{{query}}"}}}},
		},
	}}
}

func compile(t *testing.T, def m) *Model {
	t.Helper()
	model, err := Compile(&meta.LTRModel{InferenceConfig: extractors(), Definition: def})
	require.NoError(t, err)
	return model
}

// decode parses JSON the way request bodies are, numbers as float64.
func decode(t *testing.T, s string) m {
	t.Helper()
	var v m
	require.NoError(t, json.Unmarshal([]byte(s), &v))
	return v
}

func TestCompile_Features(t *testing.T) {
	model := compile(t, m{"linear": m{"weights": l{1.0, 1.0}}})
	require.Len(t, model.Features, 2)
	assert.Equal(t, "title", model.Features[0].Name)
	assert.Equal(t, 0.0, model.Features[0].Default)
	assert.True(t, math.IsNaN(model.Features[1].Default), "no default_score keeps the value missing")
}

func TestModel_Queries(t *testing.T) {
	model := compile(t, m{"linear": m{"weights": l{1.0, 1.0}}})

	t.Run("params and default params", func(t *testing.T) {
		qs, err := model.Queries(m{"query": "fox"})
		require.NoError(t, err)
		assert.Equal(t, m{"match": m{"title": "fox"}}, qs[0])
		assert.Equal(t, m{"match": m{"body": "fox"}}, qs[1])
	})
	t.Run("params override default params", func(t *testing.T) {
		qs, err := model.Queries(m{"query": "fox", "field": "summary"})
		require.NoError(t, err)
		assert.Equal(t, m{"match": m{"summary": "fox"}}, qs[1])
	})
	t.Run("missing param", func(t *testing.T) {
		_, err := model.Queries(nil)
		assert.ErrorContains(t, err, "missing param [query]")
	})
	t.Run("templates are not changed", func(t *testing.T) {
		_, err := model.Queries(m{"query": "fox"})
		require.NoError(t, err)
		assert.Equal(t, m{"match": m{"title": "{{query}}"}}, model.Features[0].Query)
	})
}

func TestRender(t *testing.T) {
	params := m{"q": "fox", "n": 3.0, "tags": l{"a", "b"}}
	tests := []struct {
		name string
		in   interface{}
		want interface{}
		err  string
	}{
		{"whole string keeps the type", "{{n}}", 3.0, ""},
		{"whole string with spaces", "{{ tags }}", l{"a", "b"}, ""},
		{"embedded", "q={{q}}, n={{n}}", "q=fox, n=3", ""},
		{"array", l{"{{q}}", 1.0, true}, l{"fox", 1.0, true}, ""},
		{"key", m{"{{q}}_x": "{{n}}"}, m{"fox_x": 3.0}, ""},
		{"no placeholder", "plain", "plain", ""},
		{"missing embedded", "a {{x}}", nil, "missing param [x]"},
		{"missing in array", l{"{{x}}"}, nil, "missing param [x]"},
		{"missing in key", m{"{{x}}": 1.0}, nil, "missing param [x]"},
		{"missing in value", m{"k": "{{x}}"}, nil, "missing param [x]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(tt.in, params)
			if tt.err != "" {
				assert.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLinear(t *testing.T) {
	tests := []struct {
		name string
		def  m
		x    []float64
		want float64
	}{
		{"weights by name", m{"weights": m{"title": 2.0, "body": 0.5}, "bias": 1.0}, []float64{3, 4}, 9},
		{"weights as list", m{"weights": l{2.0, 0.5}, "intercept": -1.0}, []float64{3, 4}, 7},
		{"missing value counts 0", m{"weights": l{2.0, 0.5}}, []float64{3, nan}, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := compile(t, m{"linear": tt.def})
			assert.InDelta(t, tt.want, model.Score(tt.x), 1e-9)
		})
	}
}

// esTree is an Elasticsearch tree splitting title at 1, then body at 2.
const esTree = `{"tree": {"feature_names": ["title", "body"], "tree_structure": [
	{"node_index": 0, "split_feature": 0, "threshold": 1, "decision_type": "lte", "default_left": false, "left_child": 1, "right_child": 2},
	{"node_index": 1, "leaf_value": [0.1]},
	{"node_index": 2, "split_feature": 1, "threshold": 2, "left_child": 3, "right_child": 4},
	{"node_index": 3, "leaf_value": 0.5},
	{"node_index": 4, "leaf_value": 0.9}
]}}`

func TestTrainedModel_Tree(t *testing.T) {
	model := compile(t, m{"trained_model": decode(t, esTree)})
	tests := []struct {
		name string
		x    []float64
		want float64
	}{
		{"left on lte", []float64{1, 0}, 0.1},
		{"right then left", []float64{2, 2}, 0.5},
		{"right then right", []float64{2, 3}, 0.9},
		{"missing goes default_left false", []float64{nan, 0}, 0.5},
		{"missing goes default_left true by default", []float64{5, nan}, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want, model.Score(tt.x), 1e-9)
		})
	}
}

func TestTrainedModel_DecisionTypes(t *testing.T) {
	tests := []struct {
		op    string
		value float64
		left  bool
	}{
		{"lt", 1, false}, {"lt", 0, true},
		{"lte", 1, true}, {"lte", 2, false},
		{"gt", 2, true}, {"gt", 1, false},
		{"gte", 1, true}, {"gte", 0, false},
		{"LTE", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			def := m{"tree": m{"tree_structure": l{
				m{"node_index": 0.0, "split_feature": 0.0, "threshold": 1.0, "decision_type": tt.op, "left_child": 1.0, "right_child": 2.0},
				m{"node_index": 1.0, "leaf_value": 1.0},
				m{"node_index": 2.0, "leaf_value": 2.0},
			}}}
			model := compile(t, m{"trained_model": def})
			want := 2.0
			if tt.left {
				want = 1
			}
			assert.Equal(t, want, model.Score([]float64{tt.value, 0}))
		})
	}
}

func TestTrainedModel_Ensemble(t *testing.T) {
	leaf := func(v float64) m {
		return m{"tree": m{"tree_structure": l{m{"node_index": 0.0, "leaf_value": v}}}}
	}
	tests := []struct {
		name string
		agg  interface{}
		want float64
	}{
		{"default weighted_sum", nil, 3},
		{"weighted_sum weights", m{"weighted_sum": m{"weights": l{2.0, 0.5}}}, 3},
		{"weighted_sum without weights", m{"weighted_sum": m{}}, 3},
		{"logistic_regression", m{"logistic_regression": m{"weights": l{1.0, -1.0}}}, 1 / (1 + math.Exp(1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ens := m{"feature_names": l{"title", "body"}, "trained_models": l{leaf(1), leaf(2)}}
			if tt.agg != nil {
				ens["aggregate_output"] = tt.agg
			}
			model := compile(t, m{"trained_model": m{"ensemble": ens}})
			assert.InDelta(t, tt.want, model.Score([]float64{0, 0}), 1e-9)
		})
	}
}

// xgbDump is booster.get_dump(dump_format="json") of two trees.
const xgbDump = `[
	{"nodeid": 0, "depth": 0, "split": "title", "split_condition": 1.5, "yes": 1, "no": 2, "missing": 2, "children": [
		{"nodeid": 1, "leaf": 0.2},
		{"nodeid": 2, "leaf": 0.7}
	]},
	{"nodeid": 0, "depth": 0, "split": "f1", "split_condition": 3, "yes": 1, "no": 2, "missing": 1, "children": [
		{"nodeid": 1, "leaf": -0.1},
		{"nodeid": 2, "leaf": 0.4}
	]}
]`

func TestXGBoost(t *testing.T) {
	var trees l
	require.NoError(t, json.Unmarshal([]byte(xgbDump), &trees))
	tests := []struct {
		name string
		def  interface{}
		x    []float64
		want float64
	}{
		{"yes on lt", trees, []float64{1, 1}, 0.1},
		{"no and f<N> names", trees, []float64{2, 3}, 1.1},
		{"missing branch", trees, []float64{nan, nan}, 0.6},
		{"splits object with base_score", m{"splits": trees, "base_score": 0.5, "objective": "rank:pairwise"}, []float64{1, 1}, 0.6},
		{"logistic objective", m{"splits": trees, "objective": "binary:logistic"}, []float64{1, 1}, 1 / (1 + math.Exp(-0.1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := compile(t, m{"xgboost": tt.def})
			assert.InDelta(t, tt.want, model.Score(tt.x), 1e-9)
		})
	}
}

func TestCompile_Errors(t *testing.T) {
	leafTree := m{"tree": m{"tree_structure": l{m{"node_index": 0.0, "leaf_value": 1.0}}}}
	split := func(extra m) m {
		n := m{"node_index": 0.0, "split_feature": 0.0, "threshold": 1.0, "left_child": 1.0, "right_child": 2.0}
		for k, v := range extra {
			if v == nil {
				delete(n, k)
			} else {
				n[k] = v
			}
		}
		return m{"tree": m{"tree_structure": l{n, m{"node_index": 1.0, "leaf_value": 1.0}, m{"node_index": 2.0, "leaf_value": 2.0}}}}
	}
	xgbLeaf := m{"nodeid": 0.0, "leaf": 1.0}
	tests := []struct {
		name string
		cfg  m
		def  m
		err  string
	}{
		{"no learning_to_rank", m{}, m{"linear": m{}}, "[inference_config.learning_to_rank] must be an object"},
		{"bad default_params", m{"learning_to_rank": m{"default_params": 1.0, "feature_extractors": l{}}}, nil, "[default_params] must be an object"},
		{"no extractors", m{"learning_to_rank": m{}}, nil, "[feature_extractors] must be an array"},
		{"empty extractors", m{"learning_to_rank": m{"feature_extractors": l{}}}, nil, "must not be empty"},
		{"extractor not object", m{"learning_to_rank": m{"feature_extractors": l{1.0}}}, nil, "[feature_extractors] must be an object"},
		{"unknown extractor", m{"learning_to_rank": m{"feature_extractors": l{m{"x": m{}}}}}, nil, "[query_extractor] must be an object"},
		{"no feature_name", m{"learning_to_rank": m{"feature_extractors": l{m{"query_extractor": m{"query": m{}}}}}}, nil, "requires [feature_name]"},
		{"no query", m{"learning_to_rank": m{"feature_extractors": l{m{"query_extractor": m{"feature_name": "a"}}}}}, nil, "[query_extractor.query] must be an object"},
		{"bad default_score", m{"learning_to_rank": m{"feature_extractors": l{m{"query_extractor": m{"feature_name": "a", "query": m{}, "default_score": m{}}}}}}, nil, "[default_score] of [a]"},
		{"duplicate feature", m{"learning_to_rank": m{"feature_extractors": l{
			m{"query_extractor": m{"feature_name": "a", "query": m{}}}, m{"query_extractor": m{"feature_name": "a", "query": m{}}}}}}, nil, "duplicate feature [a]"},
		{"no definition", nil, m{}, "exactly one of [trained_model], [xgboost], [linear]"},
		{"two definitions", nil, m{"linear": m{}, "xgboost": l{}}, "exactly one of"},
		{"unknown definition", nil, m{"onnx": m{}}, "unknown definition [onnx]"},

		{"linear not object", nil, m{"linear": 1.0}, "[linear] must be an object"},
		{"linear bad bias", nil, m{"linear": m{"bias": "x"}}, "bias must be a number"},
		{"linear unknown field", nil, m{"linear": m{"x": 1.0}}, "[linear] unknown field [x]"},
		{"linear unknown feature", nil, m{"linear": m{"weights": m{"nope": 1.0}}}, "unknown feature [nope]"},
		{"linear bad named weight", nil, m{"linear": m{"weights": m{"title": "x"}}}, "weight of [title]"},
		{"linear weights count", nil, m{"linear": m{"weights": l{1.0}}}, "has 1 weights for 2 features"},
		{"linear bad listed weight", nil, m{"linear": m{"weights": l{1.0, "x"}}}, "weight 1 must be a number"},
		{"linear weights type", nil, m{"linear": m{"weights": 1.0}}, "weights must be an object or an array"},

		{"trained_model not object", nil, m{"trained_model": 1.0}, "[trained_model] must be an object"},
		{"trained_model empty", nil, m{"trained_model": m{}}, "exactly one of [tree], [ensemble]"},
		{"trained_model unknown", nil, m{"trained_model": m{"x": m{}}}, "exactly one of [tree], [ensemble]"},
		{"ensemble not object", nil, m{"trained_model": m{"ensemble": 1.0}}, "[ensemble] must be an object"},
		{"ensemble bad feature_names", nil, m{"trained_model": m{"ensemble": m{"feature_names": 1.0}}}, "[feature_names] must be an array"},
		{"ensemble feature_names not strings", nil, m{"trained_model": m{"ensemble": m{"feature_names": l{1.0}}}}, "must be strings"},
		{"ensemble no models", nil, m{"trained_model": m{"ensemble": m{}}}, "[ensemble.trained_models] must be an array"},
		{"ensemble empty models", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{}}}}, "must not be empty"},
		{"ensemble bad model", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{1.0}}}}, "[trained_model] must be an object"},
		{"aggregate not object", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{leafTree}, "aggregate_output": 1.0}}}, "[aggregate_output] must be an object"},
		{"aggregate two", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{leafTree}, "aggregate_output": m{"weighted_sum": m{}, "logistic_regression": m{}}}}}, "exactly one of [weighted_sum]"},
		{"aggregate unknown", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{leafTree}, "aggregate_output": m{"weighted_mode": m{}}}}}, "unsupported aggregate_output [weighted_mode]"},
		{"aggregate body", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{leafTree}, "aggregate_output": m{"weighted_sum": 1.0}}}}, "[weighted_sum] must be an object"},
		{"aggregate weights type", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{leafTree}, "aggregate_output": m{"weighted_sum": m{"weights": 1.0}}}}}, "weighted_sum.weights] must be an array"},
		{"aggregate weights count", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{leafTree}, "aggregate_output": m{"weighted_sum": m{"weights": l{}}}}}}, "has 0 weights for 1 models"},
		{"aggregate bad weight", nil, m{"trained_model": m{"ensemble": m{"trained_models": l{leafTree}, "aggregate_output": m{"weighted_sum": m{"weights": l{"x"}}}}}}, "weight 0 must be a number"},

		{"tree not object", nil, m{"trained_model": m{"tree": 1.0}}, "[tree] must be an object"},
		{"tree bad feature_names", nil, m{"trained_model": m{"tree": m{"feature_names": 1.0}}}, "[feature_names] must be an array"},
		{"tree no structure", nil, m{"trained_model": m{"tree": m{}}}, "[tree.tree_structure] must be an array"},
		{"tree node not object", nil, m{"trained_model": m{"tree": m{"tree_structure": l{1.0}}}}, "[tree_structure] must be an object"},
		{"tree no node_index", nil, m{"trained_model": m{"tree": m{"tree_structure": l{m{"leaf_value": 1.0}}}}}, "requires [node_index]"},
		{"tree fractional node_index", nil, m{"trained_model": m{"tree": m{"tree_structure": l{m{"node_index": 0.5}}}}}, "[node_index] must be an integer"},
		{"tree bad leaf", nil, m{"trained_model": m{"tree": m{"tree_structure": l{m{"node_index": 0.0, "leaf_value": "x"}}}}}, "[leaf_value] of node [0]"},
		{"tree no root", nil, m{"trained_model": m{"tree": m{"tree_structure": l{m{"node_index": 1.0, "leaf_value": 1.0}}}}}, "missing node [0]"},
		{"tree duplicate node", nil, m{"trained_model": m{"tree": m{"tree_structure": l{m{"node_index": 0.0, "leaf_value": 1.0}, m{"node_index": 0.0, "leaf_value": 1.0}}}}}, "duplicate node [0]"},
		{"tree no split_feature", nil, m{"trained_model": split(m{"split_feature": nil})}, "requires [split_feature]"},
		{"tree split_feature range", nil, m{"trained_model": split(m{"split_feature": 5.0})}, "[split_feature] 5 of node [0] is out of range"},
		{"tree unknown feature name", nil, m{"trained_model": m{"ensemble": m{"feature_names": l{"zzz"}, "trained_models": l{split(nil)}}}}, "unknown feature [zzz]"},
		{"tree no threshold", nil, m{"trained_model": split(m{"threshold": nil})}, "requires [threshold]"},
		{"tree bad threshold", nil, m{"trained_model": split(m{"threshold": "x"})}, "[threshold] must be a number"},
		{"tree bad decision_type", nil, m{"trained_model": split(m{"decision_type": "eq"})}, "unknown decision_type [eq]"},
		{"tree no left_child", nil, m{"trained_model": split(m{"left_child": nil})}, "requires [left_child]"},
		{"tree no right_child", nil, m{"trained_model": split(m{"right_child": nil})}, "requires [right_child]"},
		{"tree missing child", nil, m{"trained_model": split(m{"right_child": 9.0})}, "missing node [9]"},
		{"tree cycle", nil, m{"trained_model": split(m{"left_child": 0.0})}, "node [0] is reached more than once"},

		{"xgboost bad base_score", nil, m{"xgboost": m{"splits": l{xgbLeaf}, "base_score": "x"}}, "base_score must be a number"},
		{"xgboost bad objective", nil, m{"xgboost": m{"splits": l{xgbLeaf}, "objective": "multi:softmax"}}, "unsupported objective [multi:softmax]"},
		{"xgboost unknown field", nil, m{"xgboost": m{"splits": l{xgbLeaf}, "x": 1.0}}, "[xgboost] unknown field [x]"},
		{"xgboost not list", nil, m{"xgboost": 1.0}, "[xgboost] must be an array"},
		{"xgboost no trees", nil, m{"xgboost": l{}}, "must have trees"},
		{"xgboost tree not object", nil, m{"xgboost": l{1.0}}, "[xgboost tree] must be an object"},
		{"xgboost no nodeid", nil, m{"xgboost": l{m{"leaf": 1.0}}}, "requires [nodeid]"},
		{"xgboost bad leaf", nil, m{"xgboost": l{m{"nodeid": 0.0, "leaf": "x"}}}, "[leaf] of node [0]"},
		{"xgboost unknown split", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "f9"}}}, "unknown feature [f9]"},
		{"xgboost no split_condition", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title"}}}, "requires [split_condition]"},
		{"xgboost no yes", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title", "split_condition": 1.0}}}, "requires [yes]"},
		{"xgboost no no", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title", "split_condition": 1.0, "yes": 1.0}}}, "requires [no]"},
		{"xgboost bad missing", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title", "split_condition": 1.0, "yes": 1.0, "no": 2.0, "missing": "x"}}}, "[missing] must be an integer"},
		{"xgboost no children", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title", "split_condition": 1.0, "yes": 1.0, "no": 2.0}}}, "[children] must be an array"},
		{"xgboost child not object", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title", "split_condition": 1.0, "yes": 1.0, "no": 2.0, "children": l{1.0}}}}, "[children] must be an object"},
		{"xgboost bad child", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title", "split_condition": 1.0, "yes": 1.0, "no": 2.0, "children": l{m{}}}}}, "requires [nodeid]"},
		{"xgboost missing child", nil, m{"xgboost": l{m{"nodeid": 0.0, "split": "title", "split_condition": 1.0, "yes": 1.0, "no": 2.0, "children": l{m{"nodeid": 2.0, "leaf": 1.0}}}}}, "missing node [1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if cfg == nil {
				cfg = extractors()
			}
			_, err := Compile(&meta.LTRModel{InferenceConfig: cfg, Definition: tt.def})
			assert.ErrorContains(t, err, tt.err)
		})
	}
}

func BenchmarkXGBoost(b *testing.B) {
	var trees l
	require.NoError(b, json.Unmarshal([]byte(xgbDump), &trees))
	model, err := Compile(&meta.LTRModel{InferenceConfig: extractors(), Definition: m{"xgboost": trees}})
	require.NoError(b, err)
	x := []float64{2, 3}
	for b.Loop() {
		model.Score(x)
	}
}
