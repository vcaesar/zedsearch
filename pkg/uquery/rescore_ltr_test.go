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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func ltrTestModel() *meta.LTRModel {
	return &meta.LTRModel{
		ModelID: "m",
		InferenceConfig: m{"learning_to_rank": m{"feature_extractors": []interface{}{
			m{"query_extractor": m{"feature_name": "title", "query": m{"match": m{"title": "{{q}}"}}}},
		}}},
		Definition: m{"linear": m{"weights": m{"title": 2.0}}},
	}
}

func TestParseRescore_LTR(t *testing.T) {
	t.Run("learning_to_rank replaces scores", func(t *testing.T) {
		rs, err := ParseRescore(m{"window_size": 3.0, "learning_to_rank": m{"model_id": "m", "params": m{"q": "fox"}}})
		require.NoError(t, err)
		require.Len(t, rs, 1)
		assert.Nil(t, rs[0].Query)
		assert.Equal(t, &LTRRescore{ModelID: "m", Params: m{"q": "fox"}}, rs[0].LTR)
		assert.Equal(t, 3, rs[0].WindowSize)
	})
	t.Run("sltr combines with weights", func(t *testing.T) {
		rs, err := ParseRescore(m{"query": m{"rescore_query": m{"sltr": m{"model": "m", "params": m{"q": "fox"}}}, "query_weight": 0.0}})
		require.NoError(t, err)
		assert.Nil(t, rs[0].Query)
		assert.Equal(t, &LTRRescore{ModelID: "m", Params: m{"q": "fox"}, Combine: true}, rs[0].LTR)
		assert.Equal(t, 0.0, rs[0].QueryWeight)
	})
	t.Run("sltr beside other clauses is a plain query", func(t *testing.T) {
		q := m{"sltr": m{"model": "m"}, "boost": 1.0}
		rs, err := ParseRescore(m{"query": m{"rescore_query": q}})
		require.NoError(t, err)
		assert.Equal(t, q, rs[0].Query)
		assert.Nil(t, rs[0].LTR)
	})
}

func TestRescorer_ResolveModel(t *testing.T) {
	newRescorer := func(params map[string]interface{}) *Rescorer {
		return &Rescorer{LTR: &LTRRescore{ModelID: "m", Params: params}}
	}
	t.Run("renders the feature queries", func(t *testing.T) {
		r := newRescorer(m{"q": "fox"})
		require.NoError(t, r.ResolveModel(func(string) (*meta.LTRModel, error) { return ltrTestModel(), nil }))
		assert.Equal(t, []map[string]interface{}{{"match": m{"title": "fox"}}}, r.LTR.Queries)
		assert.Equal(t, 2.0, r.LTR.Model.Score([]float64{1}))
	})
	tests := []struct {
		name   string
		params map[string]interface{}
		lookup func(string) (*meta.LTRModel, error)
		want   string
	}{
		{"not found", nil, func(string) (*meta.LTRModel, error) { return nil, errors.ErrKeyNotFound }, "model [m] not found"},
		{"storage error", nil, func(string) (*meta.LTRModel, error) { return nil, errors.New("x", "boom") }, "boom"},
		{"bad model", nil, func(string) (*meta.LTRModel, error) { return &meta.LTRModel{}, nil }, "must be an object"},
		{"missing param", nil, func(string) (*meta.LTRModel, error) { return ltrTestModel(), nil }, "missing param [q]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorContains(t, newRescorer(tt.params).ResolveModel(tt.lookup), tt.want)
		})
	}
}
