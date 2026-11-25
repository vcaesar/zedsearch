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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func hybridZedQuery(t *testing.T, body string) *meta.ZedQuery {
	t.Helper()
	q := &meta.ZedQuery{}
	require.NoError(t, json.Unmarshal([]byte(body), q))
	return q
}

func TestHybrid_Retriever(t *testing.T) {
	match := map[string]interface{}{"match": map[string]interface{}{"title": "fox"}}
	term := map[string]interface{}{"term": map[string]interface{}{"tag": "x"}}

	t.Run("rrf of standard and knn", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"retriever": {"rrf": {
			"retrievers": [{"standard": {"query": {"match": {"title": "fox"}}}}, {"knn": {"field": "emb", "query_vector": [1, 0], "k": 2}}],
			"rank_window_size": 20, "rank_constant": 10}}}`))
		require.NoError(t, err)
		assert.Equal(t, CombineRRF, r.Combination)
		assert.Equal(t, 20, r.WindowSize)
		assert.Equal(t, 10, r.RankConstant)
		assert.Equal(t, []float64{1, 1}, r.Weights)
		require.Len(t, r.Retrievers, 2)
		assert.Equal(t, match, r.Retrievers[0].Query)
		assert.Equal(t, "emb", r.Retrievers[1].Query["knn"].(map[string]interface{})["field"])
		assert.Equal(t, []interface{}{match, r.Retrievers[1].Query}, r.Leaves())
	})

	t.Run("filters reach every leaf", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"retriever": {"rrf": {"filter": {"term": {"tag": "x"}}, "retrievers": [
			{"standard": {"query": {"match": {"title": "fox"}}, "filter": [{"exists": {"field": "title"}}]}},
			{"knn": {"field": "emb", "query_vector": [1, 0], "filter": {"match_all": {}}}},
			{"knn": {"field": "emb", "query_vector": [0, 1]}}]}}}`))
		require.NoError(t, err)
		exists := map[string]interface{}{"exists": map[string]interface{}{"field": "title"}}
		assert.Equal(t, map[string]interface{}{"bool": map[string]interface{}{"must": match, "filter": []interface{}{term, exists}}}, r.Retrievers[0].Query)
		all := map[string]interface{}{"match_all": map[string]interface{}{}}
		assert.Equal(t, []interface{}{term, all}, r.Retrievers[1].Query["knn"].(map[string]interface{})["filter"])
		assert.Equal(t, []interface{}{term}, r.Retrievers[2].Query["knn"].(map[string]interface{})["filter"])
	})

	t.Run("filters join field-keyed knn options", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"retriever": {"rrf": {"filter": {"term": {"tag": "x"}}, "retrievers": [
			{"knn": {"emb": {"vector": [1, 0], "filter": {"match_all": {}}}}},
			{"knn": {"emb": {"vector": [0, 1]}}}]}}}`))
		require.NoError(t, err)
		all := map[string]interface{}{"match_all": map[string]interface{}{}}
		for i, want := range [][]interface{}{{term, all}, {term}} {
			knn := r.Retrievers[i].Query["knn"].(map[string]interface{})
			require.Len(t, knn, 1, "the field stays the only key")
			assert.Equal(t, want, knn["emb"].(map[string]interface{})["filter"])
		}
	})

	t.Run("knn without parent filters is unchanged", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"retriever": {"rrf": {"retrievers": [{"knn": {"emb": {"vector": [1, 0]}}}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, map[string]interface{}{"emb": map[string]interface{}{"vector": []interface{}{1.0, 0.0}}}, r.Retrievers[0].Query["knn"])
	})

	t.Run("weighted linear with normalizers", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"retriever": {"linear": {"normalizer": "l2_norm", "min_score": 0.5, "retrievers": [
			{"retriever": {"standard": {"query": {"match": {"title": "fox"}}}}, "weight": 2, "normalizer": "MinMax"},
			{"retriever": {"standard": {}}},
			{"standard": {"query": {"term": {"tag": "x"}}}}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, CombineSum, r.Combination)
		assert.Equal(t, []float64{2, 1, 1}, r.Weights)
		assert.Equal(t, []string{NormalizeMinMax, NormalizeL2, NormalizeL2}, r.Normalizers)
		assert.Equal(t, 0.5, *r.MinScore)
		assert.Equal(t, map[string]interface{}{"match_all": map[string]interface{}{}}, r.Retrievers[1].Query)
	})

	t.Run("nested fusion", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"retriever": {"rrf": {"retrievers": [
			{"linear": {"retrievers": [{"standard": {}}, {"standard": {"query": {"term": {"tag": "x"}}}}]}},
			{"standard": {"query": {"match": {"title": "fox"}}}}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, CombineSum, r.Retrievers[0].Combination)
		assert.Len(t, r.Leaves(), 3)
	})

	t.Run("a single retriever becomes the query", func(t *testing.T) {
		q := hybridZedQuery(t, `{"retriever": {"standard": {"query": {"match": {"title": "fox"}}}}}`)
		r, err := Hybrid(q)
		require.NoError(t, err)
		assert.Nil(t, r)
		assert.Nil(t, q.Retriever)
		assert.Equal(t, match, q.Query)
	})

	t.Run("a plain search has no fusion", func(t *testing.T) {
		for _, body := range []string{`{"query": {"match": {"title": "fox"}}}`, `{}`, `{"query": {"hybrid": {}, "match_all": {}}}`} {
			r, err := Hybrid(hybridZedQuery(t, body))
			require.NoError(t, err, body)
			assert.Nil(t, r, body)
		}
	})
}

func TestHybrid_RetrieverErrors(t *testing.T) {
	tests := []struct {
		name, body, err string
	}{
		{"with query", `{"query": {"match_all": {}}, "retriever": {"standard": {}}}`, "cannot be used with [query]"},
		{"not object", `{"retriever": 1}`, "[retriever] must be an object"},
		{"two retrievers", `{"retriever": {"standard": {}, "knn": {}}}`, "exactly one retriever, got 2"},
		{"unknown retriever", `{"retriever": {"text_similarity_reranker": {}}}`, "unknown retriever [text_similarity_reranker]"},
		{"body not object", `{"retriever": {"standard": 1}}`, "[standard] must be an object"},
		{"standard query not object", `{"retriever": {"standard": {"query": 1}}}`, "[standard.query] must be an object"},
		{"standard unknown field", `{"retriever": {"standard": {"sort": "x"}}}`, "[standard] unknown field [sort]"},
		{"rrf without retrievers", `{"retriever": {"rrf": {}}}`, "requires at least one retriever"},
		{"rrf retrievers not array", `{"retriever": {"rrf": {"retrievers": {}}}}`, "retrievers must be an array"},
		{"rrf bad window", `{"retriever": {"rrf": {"rank_window_size": 0, "retrievers": [{"standard": {}}]}}}`, "rrf.rank_window_size] must be a positive integer"},
		{"rrf fractional window", `{"retriever": {"rrf": {"rank_window_size": 2.5, "retrievers": [{"standard": {}}]}}}`, "must be a positive integer, got 2.5"},
		{"standard filter not object", `{"retriever": {"standard": {"filter": [1]}}}`, "[filter] must be an object"},
		{"rrf filter not object", `{"retriever": {"rrf": {"filter": "x", "retrievers": [{"standard": {}}]}}}`, "[filter] must be an object"},
		{"knn filter not object", `{"retriever": {"rrf": {"filter": {"match_all": {}}, "retrievers": [{"knn": {"field": "emb", "filter": [1]}}]}}}`, "[filter] must be an object"},
		{"field-keyed knn filter not object", `{"retriever": {"rrf": {"filter": {"match_all": {}}, "retrievers": [{"knn": {"emb": {"filter": 1}}}]}}}`, "[filter] must be an object"},
		{"rrf bad constant", `{"retriever": {"rrf": {"rank_constant": -1, "retrievers": [{"standard": {}}]}}}`, "[rrf.rank_constant] must be a positive integer"},
		{"rrf normalizer", `{"retriever": {"rrf": {"normalizer": "minmax", "retrievers": [{"standard": {}}]}}}`, "[rrf] unknown field [normalizer]"},
		{"linear rank constant", `{"retriever": {"linear": {"rank_constant": 1, "retrievers": [{"standard": {}}]}}}`, "[linear] unknown field [rank_constant]"},
		{"linear bad normalizer", `{"retriever": {"linear": {"normalizer": "z", "retrievers": [{"standard": {}}]}}}`, "unknown normalizer [z]"},
		{"bad min score", `{"retriever": {"linear": {"min_score": "x", "retrievers": [{"standard": {}}]}}}`, "min_score must be a number"},
		{"child not object", `{"retriever": {"rrf": {"retrievers": [1]}}}`, "[rrf.retrievers] must be an object"},
		{"negative weight", `{"retriever": {"rrf": {"retrievers": [{"retriever": {"standard": {}}, "weight": -1}]}}}`, "weight must be a non-negative number"},
		{"bad child normalizer", `{"retriever": {"linear": {"retrievers": [{"retriever": {"standard": {}}, "normalizer": 1}]}}}`, "unknown normalizer"},
		{"wrapper unknown field", `{"retriever": {"rrf": {"retrievers": [{"retriever": {"standard": {}}, "boost": 1}]}}}`, "unknown field [boost] of a retriever"},
		{"bad nested child", `{"retriever": {"rrf": {"retrievers": [{"retriever": {"nope": {}}}]}}}`, "unknown retriever [nope]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Hybrid(hybridZedQuery(t, tt.body))
			assert.ErrorContains(t, err, tt.err)
		})
	}
}

func TestHybrid_Rank(t *testing.T) {
	r, err := Hybrid(hybridZedQuery(t, `{"query": {"match": {"title": "fox"}}, "knn": [{"field": "emb", "query_vector": [1, 0]}, {"field": "emb", "query_vector": [0, 1]}],
		"rank": {"rrf": {"rank_window_size": 30, "rank_constant": 5}}}`))
	require.NoError(t, err)
	assert.Equal(t, CombineRRF, r.Combination)
	assert.Equal(t, 30, r.WindowSize)
	assert.Equal(t, 5, r.RankConstant)
	assert.Len(t, r.Retrievers, 3)
	assert.Equal(t, []float64{1, 1, 1}, r.Weights)

	r, err = Hybrid(hybridZedQuery(t, `{"query": {"match_all": {}}, "knn": {"field": "emb", "query_vector": [1, 0]}, "rank": {"rrf": {"window_size": 3}}}`))
	require.NoError(t, err)
	assert.Equal(t, 3, r.WindowSize)
	assert.Equal(t, DefaultRankConstant, r.RankConstant)

	r, err = Hybrid(hybridZedQuery(t, `{"query": {"match_all": {}}, "knn": {"field": "emb", "query_vector": [1, 0]}, "rank": {"rrf": {"window_size": 4, "rank_window_size": 4}}}`))
	require.NoError(t, err)
	assert.Equal(t, 4, r.WindowSize, "equal aliases agree")

	tests := []struct {
		name, body, err string
	}{
		{"not object", `{"rank": 1}`, "[rank] must be an object"},
		{"not rrf", `{"rank": {"linear": {}}}`, "[rank] supports only [rrf]"},
		{"rrf not object", `{"rank": {"rrf": 1}}`, "[rank.rrf] must be an object"},
		{"unknown field", `{"rank": {"rrf": {"x": 1}}}`, "[rrf] unknown field [x]"},
		{"bad window", `{"rank": {"rrf": {"rank_window_size": "x"}}}`, "must be a positive integer"},
		{"conflicting windows", `{"rank": {"rrf": {"window_size": 10, "rank_window_size": 30}}}`, "conflicts with"},
		{"query not object", `{"query": "x", "knn": {}, "rank": {"rrf": {}}}`, "[query] must be an object"},
		{"knn not object", `{"query": {"match_all": {}}, "knn": [1], "rank": {"rrf": {}}}`, "[knn] must be an object"},
		{"one search", `{"query": {"match_all": {}}, "rank": {"rrf": {}}}`, "requires a [query] and a [knn]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Hybrid(hybridZedQuery(t, tt.body))
			assert.ErrorContains(t, err, tt.err)
		})
	}
}

func TestHybrid_OpenSearch(t *testing.T) {
	queries := `"queries": [{"match": {"title": "fox"}}, {"knn": {"emb": {"vector": [1, 0], "k": 2}}}]`

	t.Run("defaults without pipeline", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"query": {"hybrid": {`+queries+`, "filter": {"term": {"tag": "x"}}, "pagination_depth": 40}}}`))
		require.NoError(t, err)
		assert.Equal(t, CombineArithmetic, r.Combination)
		assert.Equal(t, 40, r.WindowSize)
		assert.Equal(t, []float64{1, 1}, r.Weights)
		assert.Equal(t, []string{NormalizeMinMax, NormalizeMinMax}, r.Normalizers)
		assert.Contains(t, r.Retrievers[0].Query, "bool")
		knn := r.Retrievers[1].Query["knn"].(map[string]interface{})
		require.Len(t, knn, 1)
		assert.Equal(t, []interface{}{map[string]interface{}{"term": map[string]interface{}{"tag": "x"}}},
			knn["emb"].(map[string]interface{})["filter"], "a knn query filters before neighbor selection")
	})

	t.Run("knn with other keys is a bool filter", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"query": {"hybrid": {"filter": {"term": {"tag": "x"}}, "queries": [{"knn": 1, "boost": 2}]}}}`))
		require.NoError(t, err)
		assert.Contains(t, r.Retrievers[0].Query, "bool")
	})

	t.Run("normalization processor", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"query": {"hybrid": {`+queries+`}}, "search_pipeline": {"description": "d", "phase_results_processors": [
			{"normalization-processor": {"tag": "t", "normalization": {"technique": "l2"}, "combination": {"technique": "harmonic_mean", "parameters": {"weights": [0.3, 0.7]}}}}]}}`))
		require.NoError(t, err)
		assert.Equal(t, CombineHarmonic, r.Combination)
		assert.Equal(t, []float64{0.3, 0.7}, r.Weights)
		assert.Equal(t, []string{NormalizeL2, NormalizeL2}, r.Normalizers)
	})

	t.Run("score ranker processor", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"query": {"hybrid": {`+queries+`}}, "search_pipeline": {"phase_results_processors": [
			{"score-ranker-processor": {"combination": {"technique": "rrf", "rank_constant": 7}}}]}}`))
		require.NoError(t, err)
		assert.Equal(t, CombineRRF, r.Combination)
		assert.Equal(t, 7, r.RankConstant)
	})

	t.Run("default technique", func(t *testing.T) {
		r, err := Hybrid(hybridZedQuery(t, `{"query": {"hybrid": {`+queries+`}}, "search_pipeline": {"phase_results_processors": [
			{"normalization-processor": {"normalization": {}}}]}}`))
		require.NoError(t, err)
		assert.Equal(t, CombineArithmetic, r.Combination)
		assert.Equal(t, NormalizeMinMax, r.Normalizers[0])
	})

	pipeline := func(processor string) string {
		return `{"query": {"hybrid": {` + queries + `}}, "search_pipeline": {"phase_results_processors": [` + processor + `]}}`
	}
	tests := []struct {
		name, body, err string
	}{
		{"not object", `{"query": {"hybrid": 1}}`, "[hybrid] must be an object"},
		{"top-level knn", `{"query": {"hybrid": {` + queries + `}}, "knn": {"field": "emb", "query_vector": [1, 0]}}`, "[knn] cannot be used with a [hybrid] query"},
		{"filter not object", `{"query": {"hybrid": {"filter": [{}, 1], ` + queries + `}}}`, "[filter] must be an object"},
		{"knn filter not object", `{"query": {"hybrid": {"filter": {"match_all": {}}, "queries": [{"knn": {"emb": {"filter": 1}}}]}}}`, "[filter] must be an object"},
		{"fractional depth", `{"query": {"hybrid": {"pagination_depth": 1.5, ` + queries + `}}}`, "pagination_depth] must be a positive integer"},
		{"technique with unknown field", pipeline(`{"normalization-processor": {"normalization": {"technique": "l2", "x": 1}}}`), "[normalization] unknown field [x]"},
		{"empty weights", pipeline(`{"normalization-processor": {"combination": {"parameters": {"weights": []}}}}`), "number of weights [0] must match the number of queries [2]"},
		{"queries not array", `{"query": {"hybrid": {"queries": {}}}}`, "queries must be an array"},
		{"no queries", `{"query": {"hybrid": {"queries": []}}}`, "requires 1 to 5 queries, got 0"},
		{"too many queries", `{"query": {"hybrid": {"queries": [{}, {}, {}, {}, {}, {}]}}}`, "got 6"},
		{"query not object", `{"query": {"hybrid": {"queries": [1]}}}`, "[hybrid.queries] must be an object"},
		{"unknown field", `{"query": {"hybrid": {"boost": 1, ` + queries + `}}}`, "[hybrid] unknown field [boost]"},
		{"bad depth", `{"query": {"hybrid": {"pagination_depth": 0, ` + queries + `}}}`, "pagination_depth] must be a positive integer"},
		{"pipeline not object", `{"query": {"hybrid": {` + queries + `}}, "search_pipeline": "p"}`, "[search_pipeline] must be an object"},
		{"pipeline unsupported", `{"query": {"hybrid": {` + queries + `}}, "search_pipeline": {"request_processors": []}}`, "unsupported [request_processors]"},
		{"two processors", `{"query": {"hybrid": {` + queries + `}}, "search_pipeline": {"phase_results_processors": [{}, {}]}}`, "must hold one processor"},
		{"processor not object", pipeline(`1`), "[phase_results_processors] must be an object"},
		{"processor two types", pipeline(`{}`), "exactly one type"},
		{"unknown processor", pipeline(`{"collapse": {}}`), "unsupported processor [collapse]"},
		{"processor body", pipeline(`{"normalization-processor": 1}`), "[normalization-processor] must be an object"},
		{"processor field", pipeline(`{"score-ranker-processor": {"normalization": {}}}`), "unknown field [normalization]"},
		{"normalization not object", pipeline(`{"normalization-processor": {"normalization": 1}}`), "[normalization] must be an object"},
		{"normalization field", pipeline(`{"normalization-processor": {"normalization": {"x": 1}}}`), "[normalization] unknown field [x]"},
		{"z score", pipeline(`{"normalization-processor": {"normalization": {"technique": "z_score"}}}`), "unsupported technique [z_score]"},
		{"combination not object", pipeline(`{"normalization-processor": {"combination": 1}}`), "combination] must be an object"},
		{"rrf in normalization", pipeline(`{"normalization-processor": {"combination": {"technique": "rrf"}}}`), "unsupported combination technique [rrf]"},
		{"mean in ranker", pipeline(`{"score-ranker-processor": {"combination": {"technique": "arithmetic_mean"}}}`), "unsupported combination technique"},
		{"bad rank constant", pipeline(`{"score-ranker-processor": {"combination": {"rank_constant": 0}}}`), "[rank_constant] must be a positive integer"},
		{"combination field", pipeline(`{"normalization-processor": {"combination": {"rank_constant": 1}}}`), "unknown combination field [rank_constant]"},
		{"parameters not object", pipeline(`{"normalization-processor": {"combination": {"parameters": 1}}}`), "parameters] must be an object"},
		{"parameters field", pipeline(`{"normalization-processor": {"combination": {"parameters": {"x": []}}}}`), "supports only an array of [weights]"},
		{"bad weight", pipeline(`{"normalization-processor": {"combination": {"parameters": {"weights": ["a"]}}}}`), "weight must be a non-negative number"},
		{"weights count", pipeline(`{"normalization-processor": {"combination": {"parameters": {"weights": [1]}}}}`), "number of weights [1] must match the number of queries [2]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Hybrid(hybridZedQuery(t, tt.body))
			assert.ErrorContains(t, err, tt.err)
		})
	}
}

func ids(hits []RankedHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func TestRetriever_Fuse(t *testing.T) {
	hits := func(pairs ...interface{}) []RankedHit {
		var out []RankedHit
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, RankedHit{Index: "i", ID: pairs[i].(string), Score: pairs[i+1].(float64)})
		}
		return out
	}
	none := []string{NormalizeNone, NormalizeNone}
	lists := [][]RankedHit{hits("a", 10.0, "b", 5.0, "c", 0.0), hits("c", 2.0, "a", 1.0)}

	t.Run("rrf", func(t *testing.T) {
		r := &Retriever{Combination: CombineRRF, RankConstant: 60, Weights: []float64{1, 1}, Normalizers: none}
		got := r.Fuse(lists, 10)
		assert.Equal(t, []string{"a", "c", "b"}, ids(got))
		assert.InDelta(t, 1.0/61+1.0/62, got[0].Score, 1e-12)
		assert.InDelta(t, 1.0/62, got[2].Score, 1e-12)

		r.Weights = []float64{1, 3}
		assert.Equal(t, []string{"c", "a", "b"}, ids(r.Fuse(lists, 10)))
		assert.Equal(t, []string{"c", "a"}, ids(r.Fuse(lists, 2)))
	})

	t.Run("linear minmax", func(t *testing.T) {
		r := &Retriever{Combination: CombineSum, Weights: []float64{1, 2}, Normalizers: []string{NormalizeMinMax, NormalizeMinMax}}
		got := r.Fuse(lists, 10)
		assert.Equal(t, []string{"c", "a", "b"}, ids(got))
		assert.Equal(t, []float64{2, 1, 0.5}, []float64{got[0].Score, got[1].Score, got[2].Score})

		min := 0.9
		r.MinScore = &min
		assert.Equal(t, []string{"c", "a"}, ids(r.Fuse(lists, 10)))
	})

	t.Run("minmax of equal scores is one", func(t *testing.T) {
		r := &Retriever{Combination: CombineSum, Weights: []float64{1}, Normalizers: []string{NormalizeMinMax}}
		got := r.Fuse([][]RankedHit{hits("a", 3.0, "b", 3.0)}, 10)
		assert.Equal(t, 1.0, got[0].Score)
		assert.Equal(t, 1.0, got[1].Score)
	})

	t.Run("l2", func(t *testing.T) {
		r := &Retriever{Combination: CombineSum, Weights: []float64{1}, Normalizers: []string{NormalizeL2}}
		got := r.Fuse([][]RankedHit{hits("a", 4.0, "b", 3.0)}, 10)
		assert.InDelta(t, 0.8, got[0].Score, 1e-12)
		assert.InDelta(t, 0.6, got[1].Score, 1e-12)
		got = r.Fuse([][]RankedHit{hits("a", 0.0)}, 10)
		assert.Equal(t, 0.0, got[0].Score, "a zero norm keeps the scores")
	})

	means := [][]RankedHit{hits("a", 0.8, "b", 0.4), hits("b", 0.6, "z", 0.0)}
	tests := []struct {
		combination string
		a, b, z     float64
	}{
		{CombineArithmetic, 0.4, 0.5, 0},
		{CombineGeometric, 0.8, math.Sqrt(0.24), 0},
		{CombineHarmonic, 0.8, 2 / (1/0.4 + 1/0.6), 0},
	}
	for _, tt := range tests {
		t.Run(tt.combination, func(t *testing.T) {
			r := &Retriever{Combination: tt.combination, Weights: []float64{1, 1}, Normalizers: none}
			got := make(map[string]float64)
			for _, h := range r.Fuse(means, 10) {
				got[h.ID] = h.Score
			}
			require.Len(t, got, 3)
			require.Contains(t, got, "z", "a zero score hit is kept")
			assert.InDelta(t, tt.a, got["a"], 1e-12)
			assert.InDelta(t, tt.b, got["b"], 1e-12)
			assert.InDelta(t, tt.z, got["z"], 1e-12)
		})
	}

	t.Run("hits of other indexes are distinct", func(t *testing.T) {
		r := &Retriever{Combination: CombineRRF, RankConstant: 60, Weights: []float64{1, 1}, Normalizers: none}
		got := r.Fuse([][]RankedHit{{{Index: "x", ID: "a"}}, {{Index: "y", ID: "a"}}}, 10)
		assert.Len(t, got, 2)
	})

	t.Run("no hits", func(t *testing.T) {
		r := &Retriever{Combination: CombineRRF, RankConstant: 60, Weights: []float64{1, 1}, Normalizers: none}
		assert.Empty(t, r.Fuse([][]RankedHit{nil, nil}, 10))
	})

	t.Run("zero weights", func(t *testing.T) {
		for _, combination := range []string{CombineArithmetic, CombineGeometric, CombineHarmonic, CombineSum, CombineRRF} {
			r := &Retriever{Combination: combination, RankConstant: 60, Weights: []float64{0, 0}, Normalizers: none}
			got := r.Fuse(means, 10)
			require.Len(t, got, 3, combination)
			for _, h := range got {
				assert.Equal(t, 0.0, h.Score, combination)
			}
		}
	})

	t.Run("ties keep the first seen order", func(t *testing.T) {
		r := &Retriever{Combination: CombineSum, Weights: []float64{1}, Normalizers: []string{NormalizeNone}}
		assert.Equal(t, []string{"b", "a", "c"}, ids(r.Fuse([][]RankedHit{hits("b", 1.0, "a", 1.0, "c", 1.0)}, 10)))
	})
}
