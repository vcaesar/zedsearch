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
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// newHybridIndex indexes a, matching fox best, b, matching fox and nearest
// to [1, 0, 0], and c, the next nearest.
func newHybridIndex(t *testing.T, name string) *Index {
	t.Helper()
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	prop := meta.NewProperty("vector")
	prop.Dims = 3
	index.GetMappings().SetProperty("emb", prop)
	docs := map[string]map[string]interface{}{
		"a": {"title": "fox fox fox", "tag": "x", "emb": []interface{}{0.0, 0.0, 1.0}},
		"b": {"title": "fox", "tag": "y", "emb": []interface{}{1.0, 0.0, 0.0}},
		"c": {"title": "dog", "tag": "y", "emb": []interface{}{0.9, 0.1, 0.0}},
	}
	for id, doc := range docs {
		require.NoError(t, index.CreateDocument(id, doc, false))
	}
	waitForDocs(t, index, 3)
	return index
}

func hybridRequest(t *testing.T, body string) *meta.ZedQuery {
	t.Helper()
	q := &meta.ZedQuery{Size: 10}
	require.NoError(t, json.Unmarshal([]byte(body), q))
	return q
}

func hitIDs(resp *meta.SearchResponse) []string {
	out := make([]string, len(resp.Hits.Hits))
	for i, h := range resp.Hits.Hits {
		out[i] = h.ID
	}
	return out
}

const (
	hybridMatch = `{"match": {"title": "fox"}}`
	hybridKnn   = `{"field": "emb", "query_vector": [1, 0, 0], "k": 2}`
)

func TestSearch_Hybrid(t *testing.T) {
	name := "TestSearch_Hybrid.index_1"
	index := newHybridIndex(t, name)

	t.Run("rrf retriever", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"retriever": {"rrf": {"retrievers": [
			{"standard": {"query": `+hybridMatch+`}}, {"knn": `+hybridKnn+`}]}},
			"aggs": {"tags": {"terms": {"field": "tag"}}}}`))
		require.NoError(t, err)
		// b is second by text and first by vector, a first by text, c second by vector
		assert.Equal(t, []string{"b", "a", "c"}, hitIDs(resp))
		assert.InDelta(t, 1.0/61+1.0/62, resp.Hits.Hits[0].Score, 1e-9)
		assert.InDelta(t, 1.0/61, resp.Hits.Hits[1].Score, 1e-9)
		assert.Equal(t, resp.Hits.Hits[0].Score, resp.Hits.MaxScore)
		assert.Equal(t, 3, resp.Hits.Total.Value, "the union of the retrievers")
		assert.Equal(t, "fox", resp.Hits.Hits[0].Source.(map[string]interface{})["title"])
		assert.Contains(t, resp.Aggregations, "tags")
	})

	t.Run("pages the fused hits", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"from": 1, "size": 1, "_source": false, "retriever": {"rrf": {"retrievers": [
			{"standard": {"query": `+hybridMatch+`}}, {"knn": `+hybridKnn+`}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, hitIDs(resp))
		assert.NotContains(t, resp.Hits.Hits[0].Source, "title")

		resp, err = index.Search(hybridRequest(t, `{"from": 5, "retriever": {"rrf": {"retrievers": [{"standard": {"query": `+hybridMatch+`}}]}}}`))
		require.NoError(t, err)
		assert.Empty(t, resp.Hits.Hits)
		assert.InDelta(t, 1.0/61, resp.Hits.MaxScore, 1e-9, "max_score is the top fused hit")
	})

	t.Run("rank window cuts the hits", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"retriever": {"rrf": {"rank_window_size": 2, "retrievers": [
			{"standard": {"query": `+hybridMatch+`}}, {"knn": `+hybridKnn+`}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"b", "a"}, hitIDs(resp))
	})

	t.Run("the default window does not grow with from", func(t *testing.T) {
		var pages []string
		for from := 0; from < 3; from++ {
			resp, err := index.Search(hybridRequest(t, `{"from": `+strconv.Itoa(from)+`, "size": 1, "retriever": {"rrf": {"retrievers": [
				{"standard": {"query": `+hybridMatch+`}}, {"knn": `+hybridKnn+`}]}}}`))
			require.NoError(t, err)
			pages = append(pages, hitIDs(resp)...)
		}
		assert.Equal(t, []string{"b", "a", "c"}, pages)
	})

	t.Run("min_score counts the fused hits", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"retriever": {"rrf": {"min_score": 0.02, "retrievers": [
			{"standard": {"query": `+hybridMatch+`}}, {"knn": `+hybridKnn+`}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"b"}, hitIDs(resp), "only b is in both lists")
		assert.Equal(t, 1, resp.Hits.Total.Value)
	})

	t.Run("min_score above every fused score counts none", func(t *testing.T) {
		// a is first in both lists: 2/61, about 0.033
		resp, err := index.Search(hybridRequest(t, `{"retriever": {"rrf": {"min_score": 0.1, "retrievers": [
			{"standard": {"query": `+hybridMatch+`}}, {"standard": {"query": `+hybridMatch+`}}]}},
			"aggs": {"tags": {"terms": {"field": "tag"}}}}`))
		require.NoError(t, err)
		assert.Empty(t, resp.Hits.Hits)
		assert.Equal(t, 0, resp.Hits.Total.Value, "not the union of the retrievers")
		assert.Zero(t, resp.Hits.MaxScore)
		assert.Contains(t, resp.Aggregations, "tags", "aggregations keep the union")
	})

	t.Run("nested min_score counts the fused hits", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"retriever": {"rrf": {"retrievers": [
			{"linear": {"min_score": 1e9, "retrievers": [{"standard": {"query": `+hybridMatch+`}}]}},
			{"standard": {"query": {"term": {"tag": "y"}}}}]}}}`))
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"b", "c"}, hitIDs(resp))
		assert.Equal(t, 2, resp.Hits.Total.Value, "a matches the union but no retriever keeps it")
	})

	t.Run("knn filter of field-keyed options", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"query": {"hybrid": {"filter": {"term": {"tag": "x"}},
			"queries": [{"knn": {"emb": {"vector": [1, 0, 0], "k": 1}}}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, hitIDs(resp), "the filter applies before the neighbor selection")
	})

	t.Run("linear retriever with filter", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"retriever": {"linear": {"filter": {"term": {"tag": "y"}}, "retrievers": [
			{"retriever": {"standard": {"query": `+hybridMatch+`}}, "weight": 2, "normalizer": "minmax"},
			{"retriever": {"knn": `+hybridKnn+`}, "normalizer": "minmax"}]}}}`))
		require.NoError(t, err)
		// a is filtered out: b is the only text hit and the nearest vector
		assert.Equal(t, []string{"b", "c"}, hitIDs(resp))
		assert.InDelta(t, 3.0, resp.Hits.Hits[0].Score, 1e-9)
		assert.InDelta(t, 0.0, resp.Hits.Hits[1].Score, 1e-9)
	})

	t.Run("legacy rank rrf", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"query": `+hybridMatch+`, "knn": `+hybridKnn+`, "rank": {"rrf": {}}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"b", "a", "c"}, hitIDs(resp))
	})

	t.Run("opensearch hybrid query", func(t *testing.T) {
		query := `{"query": {"hybrid": {"queries": [` + hybridMatch + `, {"knn": {"emb": {"vector": [1, 0, 0], "k": 2}}}]}}}`
		resp, err := index.Search(hybridRequest(t, query))
		require.NoError(t, err)
		// min_max and arithmetic_mean: a is (1 + 0) / 2, b (0 + 1) / 2 and c (0 + 0) / 2
		assert.Equal(t, []string{"a", "b", "c"}, hitIDs(resp))
		assert.InDelta(t, 0.5, resp.Hits.Hits[0].Score, 1e-9)
		assert.InDelta(t, 0.5, resp.Hits.Hits[1].Score, 1e-9)
		assert.InDelta(t, 0.0, resp.Hits.Hits[2].Score, 1e-9)

		resp, err = index.Search(hybridRequest(t, `{"query": {"hybrid": {"queries": [`+hybridMatch+`, {"knn": {"emb": {"vector": [1, 0, 0], "k": 2}}}]}},
			"search_pipeline": {"phase_results_processors": [{"score-ranker-processor": {"combination": {"technique": "rrf"}}}]}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"b", "a", "c"}, hitIDs(resp))
	})

	t.Run("highlight of the fused hits", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"highlight": {"fields": {"title": {}}}, "retriever": {"rrf": {"retrievers": [
			{"standard": {"query": `+hybridMatch+`}}, {"knn": `+hybridKnn+`}]}}}`))
		require.NoError(t, err)
		require.Equal(t, []string{"b", "a", "c"}, hitIDs(resp))
		assert.Equal(t, []string{"<mark>fox</mark>"}, resp.Hits.Hits[0].Highlight["title"])
		assert.Empty(t, resp.Hits.Hits[2].Highlight)
	})

	t.Run("multi index search", func(t *testing.T) {
		resp, err := MultiSearch([]string{"TestSearch_Hybrid.*"}, hybridRequest(t, `{"retriever": {"rrf": {"retrievers": [
			{"standard": {"query": `+hybridMatch+`}}, {"knn": `+hybridKnn+`}]}}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"b", "a", "c"}, hitIDs(resp))
		assert.Equal(t, name, resp.Hits.Hits[0].Index)

		_, err = MultiSearch([]string{"TestSearch_Hybrid.*"}, hybridRequest(t, `{"retriever": {"rrf": {}}}`))
		assert.ErrorContains(t, err, "requires at least one retriever")
	})

	t.Run("single retriever is a plain search", func(t *testing.T) {
		resp, err := index.Search(hybridRequest(t, `{"retriever": {"knn": `+hybridKnn+`}}`))
		require.NoError(t, err)
		assert.Equal(t, []string{"b", "c"}, hitIDs(resp))
	})

	t.Run("errors", func(t *testing.T) {
		maxResults := config.Global.MaxResults
		config.Global.MaxResults = 100
		t.Cleanup(func() { config.Global.MaxResults = maxResults })
		rrf := `"retriever": {"rrf": {"retrievers": [{"standard": {"query": ` + hybridMatch + `}}]}}`
		tests := []struct {
			name, body, err string
		}{
			{"parse", `{"retriever": {"rrf": {}}}`, "requires at least one retriever"},
			{"rescore", `{` + rrf + `, "rescore": {"query": {"rescore_query": {"match_all": {}}}}}`, "[rescore] cannot be used"},
			{"sort", `{` + rrf + `, "sort": "-tag"}`, "[sort] other than [_score]"},
			{"page", `{` + rrf + `, "from": -1}`, "from and size must be within [0, 100]"},
			{"window", `{"from": 95, "retriever": {"rrf": {"rank_window_size": 105, "retrievers": [{"standard": {"query": ` + hybridMatch + `}}]}}}`,
				"rank window must be less than or equal to [100] but was [105]"},
			{"leaf query", `{"retriever": {"rrf": {"retrievers": [{"standard": {"query": {"bogus": {}}}}]}}}`, "bogus"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := index.Search(hybridRequest(t, tt.body))
				assert.ErrorContains(t, err, tt.err)
			})
		}
	})
}

// TestSearchHybrid_Phases drives the phases with a fake search.
func TestSearchHybrid_Phases(t *testing.T) {
	fusion := &uquery.Retriever{
		Retrievers:   []*uquery.Retriever{{Query: map[string]interface{}{"match_all": map[string]interface{}{}}}},
		Weights:      []float64{1},
		Normalizers:  []string{uquery.NormalizeNone},
		Combination:  uquery.CombineRRF,
		RankConstant: uquery.DefaultRankConstant,
	}

	t.Run("a timed out phase times out the search", func(t *testing.T) {
		calls := 0
		resp, err := searchHybrid(&meta.ZedQuery{Size: 10, Timeout: 1}, fusion, func(q *meta.ZedQuery) (*meta.SearchResponse, error) {
			calls++
			assert.Equal(t, 1, q.Timeout)
			return &meta.SearchResponse{TimedOut: true}, nil
		})
		require.NoError(t, err)
		assert.True(t, resp.TimedOut)
		assert.Equal(t, context.DeadlineExceeded.Error(), resp.Error)
		assert.Equal(t, 1, calls)
	})

	t.Run("phases share one deadline", func(t *testing.T) {
		calls := 0
		resp, err := searchHybrid(&meta.ZedQuery{Size: 10, Timeout: 1}, fusion, func(q *meta.ZedQuery) (*meta.SearchResponse, error) {
			calls++
			assert.Equal(t, 1, q.Timeout)
			time.Sleep(1100 * time.Millisecond)
			return &meta.SearchResponse{Hits: meta.Hits{Hits: []meta.Hit{{Index: "x", ID: "1"}}}}, nil
		})
		require.NoError(t, err)
		assert.True(t, resp.TimedOut)
		assert.Equal(t, 1, calls, "the union starts after the deadline")
	})

	t.Run("fetch keeps the ranked order, index and id", func(t *testing.T) {
		phase := 0
		resp, err := searchHybrid(&meta.ZedQuery{Size: 10}, fusion, func(q *meta.ZedQuery) (*meta.SearchResponse, error) {
			phase++
			switch phase {
			case 1: // rank
				return &meta.SearchResponse{Took: 1, Hits: meta.Hits{Hits: []meta.Hit{{Index: "x", ID: "1"}, {Index: "y", ID: "1"}, {Index: "x", ID: "gone"}}}}, nil
			case 2: // union
				return &meta.SearchResponse{Took: 2, Hits: meta.Hits{Total: meta.Total{Value: 3}}}, nil
			}
			assert.Equal(t, 3, q.Size, "the fetch matches the ranked pairs only")
			filter := q.Query.(map[string]interface{})["bool"].(map[string]interface{})["filter"]
			assert.Equal(t, map[string]interface{}{"bool": map[string]interface{}{"should": []interface{}{
				pairQuery("x", "1", "gone"), pairQuery("y", "1"),
			}}}, filter)
			return &meta.SearchResponse{Took: 4, Hits: meta.Hits{Hits: []meta.Hit{{Index: "y", ID: "1"}, {Index: "x", ID: "1"}, {Index: "y", ID: "gone"}}}}, nil
		})
		require.NoError(t, err)
		require.Len(t, resp.Hits.Hits, 2)
		assert.Equal(t, "x", resp.Hits.Hits[0].Index)
		assert.Equal(t, "y", resp.Hits.Hits[1].Index)
		assert.Greater(t, resp.Hits.Hits[0].Score, resp.Hits.Hits[1].Score)
		assert.Equal(t, 3, resp.Hits.Total.Value)
		assert.Equal(t, 7, resp.Took)
	})

	for _, tt := range []struct {
		name string
		fail int
	}{{"rank", 1}, {"union", 2}, {"fetch", 3}} {
		t.Run("a failed "+tt.name+" fails the search", func(t *testing.T) {
			phase := 0
			_, err := searchHybrid(&meta.ZedQuery{Size: 10}, fusion, func(*meta.ZedQuery) (*meta.SearchResponse, error) {
				phase++
				if phase == tt.fail {
					return nil, assert.AnError
				}
				return &meta.SearchResponse{Hits: meta.Hits{Hits: []meta.Hit{{Index: "x", ID: "1"}}}}, nil
			})
			assert.ErrorIs(t, err, assert.AnError)
		})
	}
}

func pairQuery(index string, ids ...interface{}) interface{} {
	return map[string]interface{}{"bool": map[string]interface{}{"filter": []interface{}{
		map[string]interface{}{"term": map[string]interface{}{"_index": index}},
		map[string]interface{}{"ids": map[string]interface{}{"values": ids}},
	}}}
}
