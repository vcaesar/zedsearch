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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestSearchHybrid(t *testing.T) {
	index := "TestSearchHybrid.index_1"
	request("DELETE", "/api/index/"+index, nil) // drop leftovers from previous run
	t.Cleanup(func() { request("DELETE", "/api/index/"+index, nil) })

	resp := request("PUT", "/api/index", bytes.NewBufferString(`{"name": "`+index+`", "storage_type": "disk",
		"mappings": {"properties": {"title": {"type": "text"}, "embedding": {"type": "dense_vector", "dims": 3}}}}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	for id, doc := range map[string]string{
		"a": `{"title": "fox fox fox", "embedding": [0, 0, 1]}`,
		"b": `{"title": "fox", "embedding": [1, 0, 0]}`,
		"c": `{"title": "dog", "embedding": [0.9, 0.1, 0]}`,
	} {
		resp := request("PUT", "/api/"+index+"/_doc/"+id, bytes.NewBufferString(doc))
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	}
	for _, id := range []string{"a", "b", "c"} {
		waitForDocument(t, index, id)
	}

	ids := func(t *testing.T, body string) []string {
		t.Helper()
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(body))
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		data := new(meta.SearchResponse)
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), data))
		out := make([]string, 0, len(data.Hits.Hits))
		for _, h := range data.Hits.Hits {
			out = append(out, h.ID)
		}
		return out
	}

	t.Run("elasticsearch rrf retriever", func(t *testing.T) {
		assert.Equal(t, []string{"b", "a", "c"}, ids(t, `{"retriever": {"rrf": {"retrievers": [
			{"standard": {"query": {"match": {"title": "fox"}}}},
			{"knn": {"field": "embedding", "query_vector": [1, 0, 0], "k": 2}}]}}}`))
	})

	t.Run("opensearch hybrid query", func(t *testing.T) {
		assert.Equal(t, []string{"b", "a", "c"}, ids(t, `{"query": {"hybrid": {"queries": [
			{"match": {"title": "fox"}}, {"knn": {"embedding": {"vector": [1, 0, 0], "k": 2}}}]}},
			"search_pipeline": {"phase_results_processors": [{"score-ranker-processor": {"combination": {"technique": "rrf"}}}]}}`))
	})

	t.Run("invalid retriever is a bad request", func(t *testing.T) {
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(`{"retriever": {"rrf": {}}}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), "requires at least one retriever")
	})
}
