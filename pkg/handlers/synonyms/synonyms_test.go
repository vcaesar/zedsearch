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

package synonyms

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	zedanalysis "github.com/vcaesar/zedsearch/pkg/uquery/analysis"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func call(t *testing.T, h zhttp.HandlerFunc, params, query map[string]string, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	c, w := utils.NewContext()
	utils.SetRequestData(c, body)
	utils.SetRequestURL(c, "/es/_synonyms", query)
	utils.SetRequestParams(c, params)
	h(c)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	return w, resp
}

func errorType(resp map[string]interface{}) interface{} {
	e, _ := resp["error"].(map[string]interface{})
	return e["type"]
}

func cleanupSet(t *testing.T, id string) {
	t.Cleanup(func() {
		err := metadata.Synonyms.Delete(id)
		assert.NoError(t, err)
	})
}

func TestSynonymsSet(t *testing.T) {
	id := "TestSynonymsSet.set_1"
	cleanupSet(t, id)
	params := map[string]string{"set_id": id}

	t.Run("create", func(t *testing.T) {
		w, resp := call(t, PutSet, params, nil, `{"synonyms_set":[{"id":"r1","synonyms":"hello, hi"},{"synonyms":"bye => goodbye"}]}`)
		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "created", resp["result"])
		details := resp["reload_analyzers_details"].(map[string]interface{})
		assert.Equal(t, []interface{}{}, details["reload_details"])
	})
	t.Run("get", func(t *testing.T) {
		w, resp := call(t, GetSet, params, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, float64(2), resp["count"])
		rules := resp["synonyms_set"].([]interface{})
		require.Len(t, rules, 2)
		assert.Equal(t, map[string]interface{}{"id": "r1", "synonyms": "hello, hi"}, rules[0])
		second := rules[1].(map[string]interface{})
		assert.NotEmpty(t, second["id"], "missing rule ids are generated")
		assert.Equal(t, "bye => goodbye", second["synonyms"])
	})
	t.Run("get page", func(t *testing.T) {
		_, resp := call(t, GetSet, params, map[string]string{"from": "1", "size": "5"}, "")
		assert.Equal(t, float64(2), resp["count"])
		assert.Len(t, resp["synonyms_set"], 1)
		_, resp = call(t, GetSet, params, map[string]string{"from": "9"}, "")
		assert.Equal(t, []interface{}{}, resp["synonyms_set"])
	})
	t.Run("update", func(t *testing.T) {
		w, resp := call(t, PutSet, params, nil, `{"synonyms_set":[{"id":"r1","synonyms":"a, b"}]}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "updated", resp["result"])
		set, err := metadata.Synonyms.Get(id)
		require.NoError(t, err)
		assert.Equal(t, []meta.SynonymRule{{ID: "r1", Synonyms: "a, b"}}, set.Rules)
	})
	t.Run("list", func(t *testing.T) {
		w, resp := call(t, ListSets, nil, map[string]string{"size": "1000"}, "")
		assert.Equal(t, http.StatusOK, w.Code)
		found := false
		for _, r := range resp["results"].([]interface{}) {
			if r.(map[string]interface{})["synonyms_set"] == id {
				found = true
				assert.Equal(t, float64(1), r.(map[string]interface{})["count"])
			}
		}
		assert.True(t, found)
		assert.GreaterOrEqual(t, resp["count"], float64(1))
	})

	badRequests := []struct {
		name string
		h    zhttp.HandlerFunc
		q    map[string]string
		body string
		typ  string
	}{
		{name: "put invalid json", h: PutSet, body: "{", typ: "parsing_exception"},
		{name: "put invalid rule", h: PutSet, body: `{"synonyms_set":[{"synonyms":"a => b => c"}]}`, typ: errorTypeIllegal},
		{name: "put duplicate ids", h: PutSet, body: `{"synonyms_set":[{"id":"x","synonyms":"a, b"},{"id":"x","synonyms":"c, d"}]}`, typ: errorTypeIllegal},
		{name: "get bad from", h: GetSet, q: map[string]string{"from": "x"}, typ: errorTypeIllegal},
		{name: "get negative size", h: GetSet, q: map[string]string{"size": "-1"}, typ: errorTypeIllegal},
		{name: "list bad size", h: ListSets, q: map[string]string{"size": "x"}, typ: errorTypeIllegal},
	}
	for _, tt := range badRequests {
		t.Run(tt.name, func(t *testing.T) {
			w, resp := call(t, tt.h, params, tt.q, tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, tt.typ, errorType(resp))
			assert.Equal(t, float64(http.StatusBadRequest), resp["status"])
		})
	}

	t.Run("delete", func(t *testing.T) {
		other := "TestSynonymsSet.set_2"
		_, _ = call(t, PutSet, map[string]string{"set_id": other}, nil, `{"synonyms_set":[]}`)
		w, resp := call(t, DeleteSet, map[string]string{"set_id": other}, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, true, resp["acknowledged"])
	})

	missing := map[string]string{"set_id": "TestSynonymsSet.missing", "rule_id": "r1"}
	for name, h := range map[string]zhttp.HandlerFunc{"get": GetSet, "delete": DeleteSet, "get rule": GetRule, "delete rule": DeleteRule} {
		t.Run(name+" missing set", func(t *testing.T) {
			w, resp := call(t, h, missing, nil, "")
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, errorTypeNotFound, errorType(resp))
			assert.Equal(t, float64(http.StatusNotFound), resp["status"])
		})
	}
}

func TestSynonymsRule(t *testing.T) {
	id := "TestSynonymsRule.set_1"
	cleanupSet(t, id)
	params := map[string]string{"set_id": id, "rule_id": "r1"}

	t.Run("put creates set", func(t *testing.T) {
		w, resp := call(t, PutRule, params, nil, `{"synonyms":"hello, hi"}`)
		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "created", resp["result"])
	})
	t.Run("put second rule", func(t *testing.T) {
		w, _ := call(t, PutRule, map[string]string{"set_id": id, "rule_id": "r2"}, nil, `{"synonyms":"x => y"}`)
		assert.Equal(t, http.StatusCreated, w.Code)
	})
	t.Run("update", func(t *testing.T) {
		w, resp := call(t, PutRule, params, nil, `{"synonyms":"hello, hey"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "updated", resp["result"])
	})
	t.Run("get", func(t *testing.T) {
		w, resp := call(t, GetRule, params, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, map[string]interface{}{"id": "r1", "synonyms": "hello, hey"}, resp)
	})
	t.Run("invalid body", func(t *testing.T) {
		w, resp := call(t, PutRule, params, nil, "{")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "parsing_exception", errorType(resp))
	})
	t.Run("invalid rule", func(t *testing.T) {
		w, resp := call(t, PutRule, params, nil, `{"synonyms":""}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, errorTypeIllegal, errorType(resp))
	})
	t.Run("delete", func(t *testing.T) {
		w, resp := call(t, DeleteRule, params, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "deleted", resp["result"])
		set, err := metadata.Synonyms.Get(id)
		require.NoError(t, err)
		assert.Equal(t, []meta.SynonymRule{{ID: "r2", Synonyms: "x => y"}}, set.Rules)
	})
	for name, h := range map[string]zhttp.HandlerFunc{"get": GetRule, "delete": DeleteRule} {
		t.Run(name+" missing rule", func(t *testing.T) {
			w, resp := call(t, h, params, nil, "")
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, errorTypeNotFound, errorType(resp))
		})
	}
}

// newSynonymsIndex creates an index whose analyzer "a" uses the synonyms set.
func newSynonymsIndex(t *testing.T, name, set string, extraFilters map[string]interface{}) *core.Index {
	t.Helper()
	index, err := core.NewIndex(name, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() {
		if _, ok := core.GetIndex(name); ok {
			assert.NoError(t, core.DeleteIndex(name))
		}
	})

	filters := map[string]interface{}{"syn": map[string]interface{}{"type": "synonym", "synonyms_set": set}}
	for k, v := range extraFilters {
		filters[k] = v
	}
	settings := &meta.IndexSettings{Analysis: &meta.IndexAnalysis{
		TokenFilter: filters,
		Analyzer: map[string]*meta.Analyzer{
			"a":     {Tokenizer: "standard", TokenFilter: []string{"lowercase", "syn"}},
			"plain": {Tokenizer: "standard"},
		},
	}}
	require.NoError(t, index.SetSettings(settings))
	if extraFilters == nil {
		analyzers, err := zedanalysis.RequestAnalyzer(settings.Analysis)
		require.NoError(t, err)
		require.NoError(t, index.SetAnalyzers(analyzers))
	}
	return index
}

func terms(index *core.Index, text string) []string {
	var out []string
	for _, tok := range index.GetAnalyzers()["a"].Analyze([]byte(text)) {
		out = append(out, string(tok.Term))
	}
	return out
}

func TestSynonymsReload(t *testing.T) {
	set := "TestSynonymsReload.set_1"
	name := "TestSynonymsReload.index_1"
	_, _ = call(t, PutSet, map[string]string{"set_id": set}, nil, `{"synonyms_set":[{"id":"r1","synonyms":"hello, hi"}]}`)
	index := newSynonymsIndex(t, name, set, nil)
	assert.Equal(t, []string{"hi", "hello"}, terms(index, "Hi"))

	wantDetails := map[string]interface{}{
		"_shards": map[string]interface{}{"total": float64(1), "successful": float64(1), "failed": float64(0)},
		"reload_details": []interface{}{map[string]interface{}{
			"index":              name,
			"reloaded_analyzers": []interface{}{"a"},
			"reloaded_node_ids":  []interface{}{},
		}},
	}

	t.Run("rule update reloads", func(t *testing.T) {
		w, resp := call(t, PutRule, map[string]string{"set_id": set, "rule_id": "r1"}, nil, `{"synonyms":"hi => greeting"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, wantDetails, resp["reload_analyzers_details"])
		assert.Equal(t, []string{"greeting"}, terms(index, "hi"))
	})
	t.Run("set update reloads", func(t *testing.T) {
		_, resp := call(t, PutSet, map[string]string{"set_id": set}, nil, `{"synonyms_set":[{"id":"r1","synonyms":"hi, yo"}]}`)
		assert.Equal(t, wantDetails, resp["reload_analyzers_details"])
		assert.Equal(t, []string{"hi", "yo"}, terms(index, "hi"))
	})
	t.Run("rule delete reloads", func(t *testing.T) {
		_, resp := call(t, DeleteRule, map[string]string{"set_id": set, "rule_id": "r1"}, nil, "")
		assert.Equal(t, wantDetails, resp["reload_analyzers_details"])
		assert.Equal(t, []string{"hi"}, terms(index, "hi"))
	})
	t.Run("delete used set", func(t *testing.T) {
		w, resp := call(t, DeleteSet, map[string]string{"set_id": set}, nil, "")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, errorTypeIllegal, errorType(resp))
		assert.Contains(t, resp["error"].(map[string]interface{})["reason"], name)
	})
	t.Run("reload search analyzers", func(t *testing.T) {
		w, resp := call(t, ReloadSearchAnalyzers, map[string]string{"target": name}, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		details := resp["reload_details"].([]interface{})
		require.Len(t, details, 1)
		assert.Equal(t, []interface{}{"a", "plain"}, details[0].(map[string]interface{})["reloaded_analyzers"])
	})
	t.Run("reload missing index", func(t *testing.T) {
		w, resp := call(t, ReloadSearchAnalyzers, map[string]string{"target": "TestSynonymsReload.missing"}, nil, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "index_not_found_exception", errorType(resp))
	})
	t.Run("delete set after index", func(t *testing.T) {
		require.NoError(t, core.DeleteIndex(name))
		w, _ := call(t, DeleteSet, map[string]string{"set_id": set}, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestSynonymsReloadFailure(t *testing.T) {
	set := "TestSynonymsReloadFailure.set_1"
	cleanupSet(t, set)
	_, _ = call(t, PutSet, map[string]string{"set_id": set}, nil, `{"synonyms_set":[]}`)
	newSynonymsIndex(t, "TestSynonymsReloadFailure.index_1", set, map[string]interface{}{"bad": map[string]interface{}{"type": "nope"}})

	_, resp := call(t, PutRule, map[string]string{"set_id": set, "rule_id": "r1"}, nil, `{"synonyms":"a, b"}`)
	assert.Equal(t, map[string]interface{}{
		"_shards":        map[string]interface{}{"total": float64(1), "successful": float64(0), "failed": float64(1)},
		"reload_details": []interface{}{},
	}, resp["reload_analyzers_details"])

	t.Run("filter unused by analyzers is not reloaded", func(t *testing.T) {
		other := "TestSynonymsReloadFailure.set_2"
		cleanupSet(t, other)
		newSynonymsIndex(t, "TestSynonymsReloadFailure.index_3", set, map[string]interface{}{
			"unused": map[string]interface{}{"type": "synonym", "synonyms_set": other},
		})
		_, resp := call(t, PutSet, map[string]string{"set_id": other}, nil, `{"synonyms_set":[]}`)
		assert.Equal(t, map[string]interface{}{
			"_shards":        map[string]interface{}{"total": float64(0), "successful": float64(0), "failed": float64(0)},
			"reload_details": []interface{}{},
		}, resp["reload_analyzers_details"])
	})
	t.Run("reload index without analysis", func(t *testing.T) {
		name := "TestSynonymsReloadFailure.index_2"
		index, err := core.NewIndex(name, "disk", 1)
		require.NoError(t, err)
		require.NoError(t, core.StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })
		w, resp := call(t, ReloadSearchAnalyzers, map[string]string{"target": name}, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		details := resp["reload_details"].([]interface{})
		require.Len(t, details, 1)
		assert.Equal(t, []interface{}{}, details[0].(map[string]interface{})["reloaded_analyzers"])
	})
}
