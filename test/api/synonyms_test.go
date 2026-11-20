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
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func synonymsSearch(t *testing.T, index, field, text string) []string {
	t.Helper()
	query := `{"query":{"match":{"` + field + `":"` + text + `"}},"size":10}`
	resp := request("POST", "/es/"+index+"/_search", strings.NewReader(query))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var result struct {
		Hits struct {
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &result))
	ids := make([]string, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		ids = append(ids, hit.ID)
	}
	sort.Strings(ids)
	return ids
}

func synonymsIndexDocs(t *testing.T, index string, docs map[string]string) {
	t.Helper()
	for id, body := range docs {
		resp := request("PUT", "/es/"+index+"/_doc/"+id, strings.NewReader(`{"body":"`+body+`"}`))
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	}
	for id := range docs {
		waitForDocument(t, index, id)
	}
}

func TestSharedServerExtraRoutes(t *testing.T) {
	for _, path := range []string{"/es/_synonyms", "/api/_synonyms", "/es/_query_rules", "/api/_query_rules"} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, path, nil)
			require.NoError(t, err)
			req.SetBasicAuth(username, password)
			req.Header.Set("Origin", "http://example.com")
			w := httptest.NewRecorder()
			server().ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

func TestSynonymsSetSearchAnalyzer(t *testing.T) {
	set := "TestSynonymsSetSearchAnalyzer.set_1"
	index := "TestSynonymsSetSearchAnalyzer.index_1"

	resp := request("PUT", "/es/_synonyms/"+set, strings.NewReader(`{"synonyms_set":[{"id":"r1","synonyms":"hello, hi"}]}`))
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())

	resp = request("PUT", "/es/"+index, strings.NewReader(`{
		"settings":{"analysis":{
			"filter":{"syn":{"type":"synonym","synonyms_set":"`+set+`","updateable":true}},
			"analyzer":{"a":{"tokenizer":"standard","filter":["lowercase","syn"]}}
		}},
		"mappings":{"properties":{"body":{"type":"text","analyzer":"standard","search_analyzer":"a"}}}
	}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	t.Cleanup(func() {
		request("DELETE", "/api/index/"+index, nil)
		request("DELETE", "/es/_synonyms/"+set, nil)
	})

	synonymsIndexDocs(t, index, map[string]string{"1": "Hello world", "2": "goodbye moon"})

	t.Run("search with synonym", func(t *testing.T) {
		require.Eventually(t, func() bool {
			return assert.ObjectsAreEqual([]string{"1"}, synonymsSearch(t, index, "body", "HI"))
		}, 10*time.Second, 50*time.Millisecond)
	})

	t.Run("rule update reloads the search analyzer", func(t *testing.T) {
		resp := request("PUT", "/es/_synonyms/"+set+"/r1", strings.NewReader(`{"synonyms":"hi, goodbye"}`))
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		var update struct {
			Result  string `json:"result"`
			Details struct {
				ReloadDetails []struct {
					Index             string   `json:"index"`
					ReloadedAnalyzers []string `json:"reloaded_analyzers"`
				} `json:"reload_details"`
			} `json:"reload_analyzers_details"`
		}
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &update))
		assert.Equal(t, "updated", update.Result)
		require.Len(t, update.Details.ReloadDetails, 1)
		assert.Equal(t, index, update.Details.ReloadDetails[0].Index)
		assert.Equal(t, []string{"a"}, update.Details.ReloadDetails[0].ReloadedAnalyzers)

		assert.Equal(t, []string{"2"}, synonymsSearch(t, index, "body", "hi"))
	})

	t.Run("get and list", func(t *testing.T) {
		resp := request("GET", "/api/_synonyms/"+set+"/r1", nil)
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.JSONEq(t, `{"id":"r1","synonyms":"hi, goodbye"}`, resp.Body.String())

		resp = request("GET", "/es/_synonyms/"+set, nil)
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, "Elasticsearch", resp.Header().Get("X-elastic-product"))
		assert.JSONEq(t, `{"count":1,"synonyms_set":[{"id":"r1","synonyms":"hi, goodbye"}]}`, resp.Body.String())

		resp = request("GET", "/es/_synonyms?size=1000", nil)
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.Contains(t, resp.Body.String(), `{"synonyms_set":"`+set+`","count":1}`)
	})

	t.Run("reload search analyzers", func(t *testing.T) {
		resp := request("POST", "/es/"+index+"/_reload_search_analyzers", nil)
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		assert.Contains(t, resp.Body.String(), `"reloaded_analyzers":["a"]`)
	})

	t.Run("delete used set", func(t *testing.T) {
		resp := request("DELETE", "/es/_synonyms/"+set, nil)
		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.Contains(t, resp.Body.String(), index)
	})

	t.Run("delete set after the index", func(t *testing.T) {
		require.Equal(t, http.StatusOK, request("DELETE", "/api/index/"+index, nil).Code)
		resp := request("DELETE", "/es/_synonyms/"+set, nil)
		assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		assert.Equal(t, http.StatusNotFound, request("GET", "/es/_synonyms/"+set, nil).Code)
	})
}

func TestSynonymsInlineIndexAnalyzer(t *testing.T) {
	index := "TestSynonymsInlineIndexAnalyzer.index_1"
	resp := request("PUT", "/es/"+index, strings.NewReader(`{
		"settings":{"analysis":{
			"filter":{"syn":{"type":"synonym_graph","synonyms":["laptop, notebook","ny, new york"]}},
			"analyzer":{"b":{"tokenizer":"standard","filter":["lowercase","syn"]}}
		}},
		"mappings":{"properties":{"body":{"type":"text","analyzer":"b"}}}
	}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	t.Cleanup(func() { request("DELETE", "/api/index/"+index, nil) })

	synonymsIndexDocs(t, index, map[string]string{"1": "My laptop", "2": "trip to NY"})

	tests := []struct {
		text string
		want []string
	}{
		{text: "notebook", want: []string{"1"}},
		{text: "laptop", want: []string{"1"}},
		{text: "york", want: []string{"2"}},
		{text: "ny", want: []string{"2"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			require.Eventually(t, func() bool {
				return assert.ObjectsAreEqual(tt.want, synonymsSearch(t, index, "body", tt.text))
			}, 10*time.Second, 50*time.Millisecond)
		})
	}

	t.Run("invalid inline rule", func(t *testing.T) {
		resp := request("PUT", "/es/TestSynonymsInlineIndexAnalyzer.index_2", strings.NewReader(`{
			"settings":{"analysis":{
				"filter":{"syn":{"type":"synonym","synonyms":["a => b => c"]}},
				"analyzer":{"b":{"tokenizer":"standard","filter":["syn"]}}
			}}
		}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code)
	})

	t.Run("unknown set", func(t *testing.T) {
		resp := request("PUT", "/es/TestSynonymsInlineIndexAnalyzer.index_3", strings.NewReader(`{
			"settings":{"analysis":{
				"filter":{"syn":{"type":"synonym","synonyms_set":"TestSynonymsInlineIndexAnalyzer.missing"}},
				"analyzer":{"b":{"tokenizer":"standard","filter":["syn"]}}
			}}
		}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.Contains(t, resp.Body.String(), "synonyms set [TestSynonymsInlineIndexAnalyzer.missing] not found")
	})
}
