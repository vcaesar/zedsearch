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

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

type rankResponse struct {
	Hits struct {
		Hits []struct {
			ID    string  `json:"_id"`
			Score float64 `json:"_score"`
		} `json:"hits"`
	} `json:"hits"`
	Suggest      map[string][]map[string]interface{} `json:"suggest"`
	Aggregations map[string]map[string]interface{}   `json:"aggregations"`
}

func rankSearch(t *testing.T, index, body string) (rankResponse, []string) {
	t.Helper()
	resp := request("POST", "/es/"+index+"/_search", strings.NewReader(body))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var out rankResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	ids := make([]string, 0, len(out.Hits.Hits))
	for _, h := range out.Hits.Hits {
		ids = append(ids, h.ID)
	}
	return out, ids
}

func TestRankAndAssociated(t *testing.T) {
	index := "TestRankAndAssociated.index_1"
	resp := request("PUT", "/es/"+index, strings.NewReader(`{
		"mappings":{"properties":{
			"title":{"type":"text"},
			"tag":{"type":"keyword"},
			"rank":{"type":"rank_feature"}
		}}
	}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	t.Cleanup(func() { request("DELETE", "/api/index/"+index, nil) })

	docs := map[string]string{
		"1": `{"title":"quick brown fox","tag":"animal","rank":1}`,
		"2": `{"title":"quick brown dog","tag":"animal","rank":5}`,
		"3": `{"title":"lazy brown dog sleeps","tag":"animal","rank":50}`,
		"4": `{"title":"search engine quick start","tag":"tech","rank":10}`,
	}
	for id, body := range docs {
		r := request("PUT", "/es/"+index+"/_doc/"+id, strings.NewReader(body))
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	}
	for id := range docs {
		waitForDocument(t, index, id)
	}
	require.Eventually(t, func() bool {
		_, ids := rankSearch(t, index, `{"query":{"match_all":{}}}`)
		return len(ids) == 4
	}, 10*time.Second, 50*time.Millisecond)

	t.Run("pinned", func(t *testing.T) {
		_, ids := rankSearch(t, index, `{"query":{"pinned":{"ids":["4"],"organic":{"match":{"title":"dog"}}}}}`)
		require.NotEmpty(t, ids)
		assert.Equal(t, "4", ids[0])
		assert.ElementsMatch(t, []string{"4", "2", "3"}, ids)
	})

	t.Run("boosting", func(t *testing.T) {
		_, ids := rankSearch(t, index, `{"query":{"boosting":{"positive":{"match":{"title":"quick"}},"negative":{"term":{"tag":"tech"}},"negative_boost":0.01}}}`)
		require.Len(t, ids, 3)
		assert.Equal(t, "4", ids[2])
	})

	t.Run("rank_feature", func(t *testing.T) {
		_, ids := rankSearch(t, index, `{"query":{"rank_feature":{"field":"rank","saturation":{"pivot":10}}}}`)
		assert.Equal(t, []string{"3", "4", "2", "1"}, ids)
	})

	t.Run("script_score", func(t *testing.T) {
		_, ids := rankSearch(t, index, `{"query":{"script_score":{"query":{"match_all":{}},"script":{"source":"doc['rank'].value * params.f","params":{"f":2}}}}}`)
		assert.Equal(t, []string{"3", "4", "2", "1"}, ids)
	})

	t.Run("rescore", func(t *testing.T) {
		_, ids := rankSearch(t, index, `{"size":2,"query":{"match":{"title":"brown"}},
			"rescore":{"window_size":10,"query":{"rescore_query":{"term":{"title":"fox"}},"rescore_query_weight":100}}}`)
		require.Len(t, ids, 2)
		assert.Equal(t, "1", ids[0])
	})

	t.Run("query rules", func(t *testing.T) {
		set := "TestRankAndAssociated.rules_1"
		r := request("PUT", "/es/_query_rules/"+set, strings.NewReader(`{"rules":[
			{"rule_id":"pin","type":"pinned","criteria":[{"type":"exact","metadata":"q","values":["dogs"]}],"actions":{"ids":["1"]}},
			{"rule_id":"drop","type":"exclude","criteria":[{"type":"always"}],"actions":{"ids":["3"]}}
		]}`))
		require.Equal(t, http.StatusCreated, r.Code, r.Body.String())
		t.Cleanup(func() { request("DELETE", "/es/_query_rules/"+set, nil) })

		r = request("POST", "/api/_query_rules/"+set+"/_test", strings.NewReader(`{"match_criteria":{"q":"dogs"}}`))
		assert.Equal(t, http.StatusOK, r.Code)
		assert.Contains(t, r.Body.String(), `"total_matched_rules":2`)

		_, ids := rankSearch(t, index, `{"query":{"rule":{"match_criteria":{"q":"dogs"},"ruleset_ids":["`+set+`"],"organic":{"match":{"title":"dog"}}}}}`)
		assert.Equal(t, []string{"1", "2"}, ids)
		_, ids = rankSearch(t, index, `{"query":{"rule":{"match_criteria":{"q":"cats"},"ruleset_ids":["`+set+`"],"organic":{"match":{"title":"dog"}}}}}`)
		assert.Equal(t, []string{"2"}, ids)

		resp := request("POST", "/es/"+index+"/_search", strings.NewReader(`{"query":{"rule":{"match_criteria":{},"ruleset_ids":["missing"],"organic":{"match_all":{}}}}}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code)
	})

	t.Run("more_like_this", func(t *testing.T) {
		_, ids := rankSearch(t, index, `{"query":{"more_like_this":{"fields":["title"],"like":[{"_index":"`+index+`","_id":"2"}],"min_term_freq":1,"min_doc_freq":1}}}`)
		require.NotEmpty(t, ids)
		assert.NotContains(t, ids, "2")
		assert.Contains(t, ids, "1")
	})

	t.Run("term suggest", func(t *testing.T) {
		out, _ := rankSearch(t, index, `{"size":0,"suggest":{"s":{"text":"quikc","term":{"field":"title"}}}}`)
		require.Len(t, out.Suggest["s"], 1)
		options := out.Suggest["s"][0]["options"].([]interface{})
		require.NotEmpty(t, options)
		assert.Equal(t, "quick", options[0].(map[string]interface{})["text"])
	})

	t.Run("completion suggest", func(t *testing.T) {
		out, _ := rankSearch(t, index, `{"size":0,"suggest":{"c":{"prefix":"te","completion":{"field":"tag"}}}}`)
		require.Len(t, out.Suggest["c"], 1)
		options := out.Suggest["c"][0]["options"].([]interface{})
		require.NotEmpty(t, options)
		assert.Equal(t, "tech", options[0].(map[string]interface{})["text"])
	})

	t.Run("significant_terms", func(t *testing.T) {
		// text fields have no doc values to aggregate, as Elasticsearch
		// without fielddata, so significance runs on the keyword field
		out, _ := rankSearch(t, index, `{"size":0,"query":{"match":{"title":"dog"}},
			"aggs":{"sig":{"significant_terms":{"field":"tag","min_doc_count":1}}}}`)
		sig := out.Aggregations["sig"]
		require.NotNil(t, sig)
		buckets := sig["buckets"].([]interface{})
		require.NotEmpty(t, buckets)
		keys := []interface{}{}
		for _, b := range buckets {
			keys = append(keys, b.(map[string]interface{})["key"])
		}
		assert.Equal(t, []interface{}{"animal"}, keys)
		assert.Equal(t, float64(4), sig["bg_count"])
	})
}
