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
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLearningToRank uploads an XGBoost model through the trained models API
// and reranks a search with it, through learning_to_rank and sltr.
func TestLearningToRank(t *testing.T) {
	index := "TestLearningToRank.index_1"
	model := "TestLearningToRank.model_1"
	resp := request("PUT", "/es/"+index, strings.NewReader(`{"mappings":{"properties":{
		"title":{"type":"text"},"popularity":{"type":"long"}}}}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	t.Cleanup(func() { request("DELETE", "/api/index/"+index, nil) })

	docs := map[string]string{
		"1": `{"title":"fox fox fox","popularity":1}`,
		"2": `{"title":"fox","popularity":100}`,
	}
	for id, body := range docs {
		r := request("PUT", "/es/"+index+"/_doc/"+id, strings.NewReader(body))
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	}
	for id := range docs {
		waitForDocument(t, index, id)
	}
	require.Eventually(t, func() bool {
		_, ids := rankSearch(t, index, `{"query":{"match":{"title":"fox"}}}`)
		return len(ids) == 2 && ids[0] == "1"
	}, 10*time.Second, 50*time.Millisecond)

	// one tree: popular documents (popularity >= 50) score 5, others 1
	r := request("PUT", "/es/_ml/trained_models/"+model, strings.NewReader(`{
		"inference_config":{"learning_to_rank":{"feature_extractors":[
			{"query_extractor":{"feature_name":"title_bm25","query":{"match":{"title":"{{q}}"}}}},
			{"query_extractor":{"feature_name":"popular","query":{"constant_score":{"filter":{"range":{"popularity":{"gte":50}}}}},"default_score":0}}
		]}},
		"definition":{"xgboost":{"objective":"reg:squarederror","splits":[
			{"nodeid":0,"split":"popular","split_condition":0.5,"yes":1,"no":2,"missing":1,"children":[
				{"nodeid":1,"leaf":1},{"nodeid":2,"leaf":5}]}
		]}}
	}`))
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	t.Cleanup(func() { request("DELETE", "/es/_ml/trained_models/"+model, nil) })

	r = request("GET", "/api/_ml/trained_models/"+model, nil)
	assert.Equal(t, http.StatusOK, r.Code, r.Body.String())

	out, ids := rankSearch(t, index, `{"query":{"match":{"title":"fox"}},
		"rescore":{"window_size":10,"learning_to_rank":{"model_id":"`+model+`","params":{"q":"fox"}}}}`)
	assert.Equal(t, []string{"2", "1"}, ids)
	assert.InDelta(t, 5.0, out.Hits.Hits[0].Score, 1e-6)
	assert.InDelta(t, 1.0, out.Hits.Hits[1].Score, 1e-6)

	_, ids = rankSearch(t, index, `{"query":{"match":{"title":"fox"}},
		"rescore":{"query":{"rescore_query":{"sltr":{"model":"`+model+`","params":{"q":"fox"}}},"query_weight":0}}}`)
	assert.Equal(t, []string{"2", "1"}, ids)

	r = request("POST", "/es/"+index+"/_search", strings.NewReader(`{"query":{"match_all":{}},
		"rescore":{"learning_to_rank":{"model_id":"TestLearningToRank.missing"}}}`))
	assert.Equal(t, http.StatusBadRequest, r.Code, r.Body.String())
	assert.Contains(t, r.Body.String(), "not found")
}
