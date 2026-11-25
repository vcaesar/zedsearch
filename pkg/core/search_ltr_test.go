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

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
)

type obj = map[string]interface{}

// storeLTRModel stores a linear model scoring 10 for tag [{{tag}}] and
// title_weight per point of the title score.
func storeLTRModel(t *testing.T, id string) {
	t.Helper()
	require.NoError(t, metadata.LTRModels.Set(id, meta.LTRModel{
		ModelID: id,
		InferenceConfig: obj{"learning_to_rank": obj{
			"default_params": obj{"title_weight": 0.0},
			"feature_extractors": []interface{}{
				obj{"query_extractor": obj{"feature_name": "title", "query": obj{"match": obj{"title": "{{q}}"}}}},
				obj{"query_extractor": obj{"feature_name": "tag", "query": obj{"constant_score": obj{"filter": obj{"term": obj{"tag": "{{tag}}"}}}}, "default_score": 0.0}},
			},
		}},
		Definition: obj{"linear": obj{"weights": obj{"title": 0.0, "tag": 10.0}, "bias": 1.0}},
	}))
	t.Cleanup(func() { assert.NoError(t, metadata.LTRModels.Delete(id)) })
}

func TestSearch_LearningToRank(t *testing.T) {
	index := newRescoreIndex(t, "TestSearch_LearningToRank.index_1")
	model := "TestSearch_LearningToRank.model_1"
	storeLTRModel(t, model)

	t.Run("model scores replace the window scores", func(t *testing.T) {
		resp, err := index.Search(rescoreQuery(obj{"learning_to_rank": obj{"model_id": model, "params": obj{"q": "fox", "tag": "y"}}}))
		require.NoError(t, err)
		require.Len(t, resp.Hits.Hits, 2)
		assert.Equal(t, "b", resp.Hits.Hits[0].ID)
		assert.InDelta(t, 11.0, resp.Hits.Hits[0].Score, 1e-6, "bias + 10 for the tag")
		assert.InDelta(t, 1.0, resp.Hits.Hits[1].Score, 1e-6, "an unmatched feature takes its default_score")
		assert.InDelta(t, 11.0, resp.Hits.MaxScore, 1e-6)
	})

	t.Run("hits past the window stay below it", func(t *testing.T) {
		resp, err := index.Search(rescoreQuery(obj{"window_size": 1, "learning_to_rank": obj{"model_id": model, "params": obj{"q": "fox", "tag": "z"}}}))
		require.NoError(t, err)
		require.Len(t, resp.Hits.Hits, 2)
		assert.Equal(t, "a", resp.Hits.Hits[0].ID)
		assert.InDelta(t, 1.0, resp.Hits.Hits[0].Score, 1e-6)
		assert.Greater(t, resp.Hits.Hits[1].Score, 0.0, "keeps its query score")
	})

	t.Run("sltr combines with the query score", func(t *testing.T) {
		resp, err := index.Search(rescoreQuery(obj{"query": obj{
			"rescore_query": obj{"sltr": obj{"model": model, "params": obj{"q": "fox", "tag": "y"}}},
			"query_weight":  0.0,
		}}))
		require.NoError(t, err)
		require.Len(t, resp.Hits.Hits, 2)
		assert.Equal(t, "b", resp.Hits.Hits[0].ID)
		assert.InDelta(t, 11.0, resp.Hits.Hits[0].Score, 1e-6)
		assert.InDelta(t, 1.0, resp.Hits.Hits[1].Score, 1e-6)
	})

	t.Run("page is cut after rescoring", func(t *testing.T) {
		q := rescoreQuery(obj{"learning_to_rank": obj{"model_id": model, "params": obj{"q": "fox", "tag": "y"}}})
		q.From, q.Size = 1, 1
		resp, err := index.Search(q)
		require.NoError(t, err)
		require.Len(t, resp.Hits.Hits, 1)
		assert.Equal(t, "a", resp.Hits.Hits[0].ID)
		assert.Equal(t, 1, q.From)
		assert.Equal(t, 1, q.Size)
	})

	errs := []struct {
		name    string
		rescore obj
		want    string
	}{
		{"unknown model", obj{"learning_to_rank": obj{"model_id": "TestSearch_LearningToRank.missing"}}, "model [TestSearch_LearningToRank.missing] not found"},
		{"missing param", obj{"learning_to_rank": obj{"model_id": model, "params": obj{"q": "fox"}}}, "missing param [tag]"},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			_, err := index.Search(rescoreQuery(tt.rescore))
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}

	t.Run("a bad feature query fails the search", func(t *testing.T) {
		require.NoError(t, metadata.LTRModels.Set(model+"_bad", meta.LTRModel{
			InferenceConfig: obj{"learning_to_rank": obj{"feature_extractors": []interface{}{
				obj{"query_extractor": obj{"feature_name": "b", "query": obj{"bogus": obj{}}}},
			}}},
			Definition: obj{"linear": obj{"weights": []interface{}{1.0}}},
		}))
		t.Cleanup(func() { assert.NoError(t, metadata.LTRModels.Delete(model+"_bad")) })
		_, err := index.Search(rescoreQuery(obj{"learning_to_rank": obj{"model_id": model + "_bad"}}))
		assert.Error(t, err)
	})

	t.Run("feature queries are transformed", func(t *testing.T) {
		require.NoError(t, metadata.LTRModels.Set(model+"_rule", meta.LTRModel{
			InferenceConfig: obj{"learning_to_rank": obj{"feature_extractors": []interface{}{
				obj{"query_extractor": obj{"feature_name": "r", "query": obj{"rule": obj{
					"ruleset_ids": []interface{}{"TestSearch_LearningToRank.missing"}, "match_criteria": obj{"q": "x"},
					"organic": obj{"match_all": obj{}},
				}}}},
			}}},
			Definition: obj{"linear": obj{"weights": []interface{}{1.0}}},
		}))
		t.Cleanup(func() { assert.NoError(t, metadata.LTRModels.Delete(model+"_rule")) })
		_, err := index.Search(rescoreQuery(obj{"learning_to_rank": obj{"model_id": model + "_rule"}}))
		assert.ErrorContains(t, err, "ruleset [TestSearch_LearningToRank.missing] not found")
	})
}
