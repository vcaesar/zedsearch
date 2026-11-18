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

package api

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestSearchVector(t *testing.T) {
	index := "TestSearchVector.index_1"
	request("DELETE", "/api/index/"+index, nil) // drop leftovers from previous run

	t.Run("reject bad vector mapping", func(t *testing.T) {
		resp := request("PUT", "/api/index", bytes.NewBufferString(`{
			"name": "`+index+`",
			"mappings": {"properties": {"embedding": {"type": "vector", "dims": -1}}}
		}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), "dims")
	})

	t.Run("create index with vector mapping", func(t *testing.T) {
		resp := request("PUT", "/api/index", bytes.NewBufferString(`{
			"name": "`+index+`",
			"storage_type": "disk",
			"mappings": {"properties": {
				"name": {"type": "keyword"},
				"embedding": {"type": "dense_vector", "dims": 3}
			}}
		}`))
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

		resp = request("GET", "/api/"+index+"/_mapping", nil)
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), `"type":"vector"`)
		assert.Contains(t, resp.Body.String(), `"dims":3`)
	})

	docs := map[string]string{
		"x":  `{"name": "x", "embedding": [1, 0, 0]}`,
		"y":  `{"name": "y", "embedding": [0, 1, 0]}`,
		"z":  `{"name": "z", "embedding": [0, 0, 1]}`,
		"xy": `{"name": "xy", "embedding": [1, 1, 0]}`,
	}
	t.Run("index documents", func(t *testing.T) {
		for id, doc := range docs {
			resp := request("PUT", "/api/"+index+"/_doc/"+id, bytes.NewBufferString(doc))
			assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		}
		for id := range docs {
			waitForDocument(t, index, id)
		}
	})

	t.Run("reject invalid vectors", func(t *testing.T) {
		for name, doc := range map[string]string{
			"wrong dims":  `{"embedding": [1, 0]}`,
			"not numeric": `{"embedding": ["a", "b", "c"]}`,
			"scalar":      `{"embedding": 1}`,
		} {
			resp := request("PUT", "/api/"+index+"/_doc/bad", bytes.NewBufferString(doc))
			// document validation errors are reported as 500 by handler, same as bad geo_point
			assert.Equal(t, http.StatusInternalServerError, resp.Code, name)
			assert.Contains(t, resp.Body.String(), "vector", name)
		}
	})

	search := func(t *testing.T, path, query string) *meta.SearchResponse {
		t.Helper()
		resp := request("POST", path, bytes.NewBufferString(query))
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		data := new(meta.SearchResponse)
		assert.NoError(t, json.Unmarshal(resp.Body.Bytes(), data))
		return data
	}
	ids := func(data *meta.SearchResponse) []string {
		out := make([]string, 0, len(data.Hits.Hits))
		for _, h := range data.Hits.Hits {
			out = append(out, h.ID)
		}
		return out
	}

	t.Run("knn cosine ordered by similarity", func(t *testing.T) {
		// k is per shard; size caps the merged result like Elasticsearch
		data := search(t, "/es/"+index+"/_search", `{"size": 2, "query": {"knn": {"field": "embedding", "query_vector": [1, 0.1, 0], "k": 2}}}`)
		assert.Equal(t, []string{"x", "xy"}, ids(data))
		assert.Greater(t, data.Hits.Hits[0].Score, data.Hits.Hits[1].Score)
	})

	t.Run("knn l2 via opensearch form", func(t *testing.T) {
		data := search(t, "/es/"+index+"/_search", `{"size": 1, "query": {"knn": {"embedding": {"vector": [0, 0, 1], "k": 1, "space_type": "l2"}}}}`)
		assert.Equal(t, []string{"z"}, ids(data))
	})

	t.Run("knn composes with bool filter", func(t *testing.T) {
		data := search(t, "/es/"+index+"/_search", `{"query": {"bool": {
			"must": {"knn": {"field": "embedding", "query_vector": [1, 1, 0], "k": 4}},
			"filter": {"term": {"name": "y"}}}}}`)
		assert.Equal(t, []string{"y"}, ids(data))
	})

	t.Run("knn ann via num_candidates", func(t *testing.T) {
		data := search(t, "/es/"+index+"/_search", `{"size": 2, "query": {"knn": {"field": "embedding", "query_vector": [1, 0.1, 0], "k": 2, "num_candidates": 10}}}`)
		assert.Equal(t, []string{"x", "xy"}, ids(data))
		assert.Greater(t, data.Hits.Hits[0].Score, data.Hits.Hits[1].Score)

		data = search(t, "/es/"+index+"/_search", `{"size": 1, "query": {"knn": {"embedding": {"vector": [0, 0, 1], "k": 1, "space_type": "l2", "method_parameters": {"ef_search": 8}}}}}`)
		assert.Equal(t, []string{"z"}, ids(data))
	})

	t.Run("knn ann composes with bool filter", func(t *testing.T) {
		data := search(t, "/es/"+index+"/_search", `{"query": {"bool": {
			"must": {"knn": {"field": "embedding", "query_vector": [1, 1, 0], "k": 4, "num_candidates": 4}},
			"filter": {"term": {"name": "y"}}}}}`)
		assert.Equal(t, []string{"y"}, ids(data))
	})

	t.Run("knn filter is a pre-filter", func(t *testing.T) {
		// a bool filter would drop the single nearest hit x and return nothing
		for _, ann := range []string{``, `, "num_candidates": 10`} {
			data := search(t, "/es/"+index+"/_search", `{"query": {"knn": {"field": "embedding", "query_vector": [1, 0, 0], "k": 1`+ann+`,
				"filter": {"term": {"name": "y"}}}}}`)
			assert.Equal(t, []string{"y"}, ids(data), ann)
		}
	})

	t.Run("knn minimum similarity", func(t *testing.T) {
		data := search(t, "/es/"+index+"/_search", `{"query": {"knn": {"field": "embedding", "query_vector": [1, 0, 0], "k": 4, "similarity": 0.5, "boost": 2}}}`)
		assert.Equal(t, []string{"x", "xy"}, ids(data))
		assert.InDelta(t, 2.0, data.Hits.Hits[0].Score, 1e-6)

		// for l2 similarity is the maximum distance
		data = search(t, "/es/"+index+"/_search", `{"query": {"knn": {"embedding": {"vector": [1, 0, 0], "k": 4, "space_type": "l2", "similarity": 1.1}}}}`)
		assert.Equal(t, []string{"x", "xy"}, ids(data))
	})

	t.Run("top-level knn", func(t *testing.T) {
		data := search(t, "/es/"+index+"/_search", `{"size": 1, "knn": {"field": "embedding", "query_vector": [0, 1, 0], "k": 1}}`)
		assert.Equal(t, []string{"y"}, ids(data))

		// query and knn hits are ORed; k is per shard, similarity keeps only y
		data = search(t, "/es/"+index+"/_search", `{"query": {"term": {"name": "z"}},
			"knn": [{"field": "embedding", "query_vector": [0, 1, 0], "k": 1, "similarity": 0.9}]}`)
		assert.ElementsMatch(t, []string{"y", "z"}, ids(data))

		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(`{"knn": [1]}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
	})

	t.Run("knn rejects num_candidates below k", func(t *testing.T) {
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(
			`{"query": {"knn": {"field": "embedding", "query_vector": [1, 0, 0], "k": 3, "num_candidates": 2}}}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), "num_candidates")
	})

	t.Run("knn rejects dims mismatch", func(t *testing.T) {
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(
			`{"query": {"knn": {"field": "embedding", "query_vector": [1, 0]}}}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), "dimensions")
	})

	t.Run("knn on non vector field", func(t *testing.T) {
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(
			`{"query": {"knn": {"field": "name", "query_vector": [1, 0, 0]}}}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
	})

	t.Run("cleanup", func(t *testing.T) {
		resp := request("DELETE", "/api/index/"+index, nil)
		assert.Equal(t, http.StatusOK, resp.Code)
	})
}

func TestSearchVectorHNSWMapping(t *testing.T) {
	index := "TestSearchVectorHNSWMapping.index_1"
	request("DELETE", "/api/index/"+index, nil) // drop leftovers from previous run

	t.Run("reject bad index_options", func(t *testing.T) {
		resp := request("PUT", "/es/"+index, bytes.NewBufferString(`{
			"mappings": {"properties": {"embedding": {"type": "dense_vector", "dims": 3, "index_options": {"type": "hnsw", "m": 1}}}}
		}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), "index_options")
	})

	t.Run("create index with hnsw mapping", func(t *testing.T) {
		resp := request("PUT", "/es/"+index, bytes.NewBufferString(`{
			"mappings": {"properties": {
				"name": {"type": "keyword"},
				"embedding": {"type": "dense_vector", "dims": 3, "similarity": "l2_norm", "index_options": {"type": "hnsw", "m": 8, "ef_construction": 32}}
			}}
		}`))
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

		resp = request("GET", "/api/"+index+"/_mapping", nil)
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), `"index_options":{"type":"hnsw","m":8,"ef_construction":32}`)
	})

	t.Run("index documents", func(t *testing.T) {
		for id, doc := range map[string]string{
			"x":  `{"name": "x", "embedding": [1, 0, 0]}`,
			"y":  `{"name": "y", "embedding": [0, 1, 0]}`,
			"xy": `{"name": "xy", "embedding": [1, 1, 0]}`,
		} {
			resp := request("PUT", "/api/"+index+"/_doc/"+id, bytes.NewBufferString(doc))
			assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			waitForDocument(t, index, id)
		}
	})

	t.Run("knn uses hnsw without num_candidates", func(t *testing.T) {
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(
			`{"size": 2, "query": {"knn": {"field": "embedding", "query_vector": [1, 0.1, 0], "k": 2}}}`))
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		data := new(meta.SearchResponse)
		assert.NoError(t, json.Unmarshal(resp.Body.Bytes(), data))
		ids := make([]string, 0, len(data.Hits.Hits))
		for _, h := range data.Hits.Hits {
			ids = append(ids, h.ID)
		}
		assert.Equal(t, []string{"x", "xy"}, ids)
	})

	t.Run("knn uses mapping similarity", func(t *testing.T) {
		// cosine ranks x first, l2 ranks xy first
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(
			`{"size": 1, "query": {"knn": {"field": "embedding", "query_vector": [10, 2, 0], "k": 1}}}`))
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		data := new(meta.SearchResponse)
		assert.NoError(t, json.Unmarshal(resp.Body.Bytes(), data))
		assert.Len(t, data.Hits.Hits, 1)
		assert.Equal(t, "xy", data.Hits.Hits[0].ID)
	})

	t.Run("cleanup", func(t *testing.T) {
		resp := request("DELETE", "/api/index/"+index, nil)
		assert.Equal(t, http.StatusOK, resp.Code)
	})
}

func TestSearchVectorExcludeSource(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
	}{
		{name: "excluded", settings: `{"index.mapping.exclude_source_vectors": true}`},
		{name: "kept", settings: `{"index": {"mapping": {"exclude_source_vectors": false}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := "TestSearchVectorExcludeSource.index_" + tc.name
			request("DELETE", "/api/index/"+index, nil) // drop leftovers from previous run

			resp := request("PUT", "/es/"+index, bytes.NewBufferString(`{
				"settings": `+tc.settings+`,
				"mappings": {"properties": {
					"name": {"type": "keyword"},
					"meta": {"properties": {"emb": {"type": "knn_vector", "dimension": 2, "compression_level": "4x"}}}
				}}
			}`))
			assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			defer request("DELETE", "/api/index/"+index, nil)

			resp = request("GET", "/api/"+index+"/_mapping", nil)
			assert.Contains(t, resp.Body.String(), `"index_options":{"type":"int8_hnsw"}`)

			resp = request("PUT", "/api/"+index+"/_doc/1", bytes.NewBufferString(`{"name": "x", "meta": {"emb": [0.5, 0.25], "tag": "t"}}`))
			assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			waitForDocument(t, index, "1")

			want := map[string]interface{}{"emb": []interface{}{0.5, 0.25}, "tag": "t"}
			getSource := func() map[string]interface{} {
				resp := request("GET", "/api/"+index+"/_doc/1", nil)
				assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
				hit := new(meta.Hit)
				assert.NoError(t, json.Unmarshal(resp.Body.Bytes(), hit))
				return hit.Source.(map[string]interface{})
			}
			assert.Equal(t, want, getSource()["meta"])

			t.Run("search restores vectors", func(t *testing.T) {
				resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(
					`{"query": {"knn": {"field": "meta.emb", "query_vector": [1, 0.5], "k": 1}}}`))
				assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
				data := new(meta.SearchResponse)
				assert.NoError(t, json.Unmarshal(resp.Body.Bytes(), data))
				if assert.Len(t, data.Hits.Hits, 1) {
					assert.Equal(t, want, data.Hits.Hits[0].Source.(map[string]interface{})["meta"])
				}
			})

			t.Run("top_hits restores vectors", func(t *testing.T) {
				resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(
					`{"size": 0, "aggs": {"top": {"top_hits": {"size": 1}}}}`))
				assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
				assert.Contains(t, resp.Body.String(), `"emb":[0.5,0.25]`)
			})

			t.Run("update restores the new vector", func(t *testing.T) {
				resp := request("POST", "/api/"+index+"/_update/1", bytes.NewBufferString(`{"name": "y", "meta": {"emb": [0.125, 1]}}`))
				assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
				assert.Eventually(t, func() bool { return getSource()["name"] == "y" }, 10*time.Second, 20*time.Millisecond)
				assert.Equal(t, map[string]interface{}{"emb": []interface{}{0.125, 1.0}}, getSource()["meta"])
			})
		})
	}
}
