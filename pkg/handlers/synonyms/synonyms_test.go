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
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

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

func TestSynonymsPageBounds(t *testing.T) {
	id := "TestSynonymsPageBounds.set_1"
	cleanupSet(t, id)
	_, _ = call(t, PutSet, map[string]string{"set_id": id}, nil, `{"synonyms_set":[{"id":"r1","synonyms":"a, b"},{"id":"r2","synonyms":"c, d"}]}`)
	maxInt := strconv.Itoa(math.MaxInt)

	tests := []struct {
		name  string
		h     zhttp.HandlerFunc
		query map[string]string
		key   string
		want  int
	}{
		{name: "get huge size", h: GetSet, query: map[string]string{"size": maxInt}, key: "synonyms_set", want: 2},
		{name: "get overflowing end", h: GetSet, query: map[string]string{"from": "1", "size": maxInt}, key: "synonyms_set", want: 1},
		{name: "list huge size", h: ListSets, query: map[string]string{"size": maxInt}, key: "results", want: -1},
		{name: "list overflowing end", h: ListSets, query: map[string]string{"from": "1", "size": maxInt}, key: "results", want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			var resp map[string]interface{}
			require.NotPanics(t, func() { w, resp = call(t, tt.h, map[string]string{"set_id": id}, tt.query, "") })
			assert.Equal(t, http.StatusOK, w.Code)
			if tt.want >= 0 {
				assert.Len(t, resp[tt.key], tt.want)
			} else {
				assert.NotNil(t, resp[tt.key])
			}
		})
	}
	t.Run("paginate", func(t *testing.T) {
		items := []int{1, 2, 3}
		assert.Equal(t, []int{2, 3}, paginate(items, 1, math.MaxInt))
		assert.Equal(t, []int{1}, paginate(items, 0, 1))
		assert.Empty(t, paginate(items, 3, 1))
		assert.Empty(t, paginate(items, 0, 0))
	})
}

func TestReloadSearchAnalyzersLocks(t *testing.T) {
	name := "TestReloadSearchAnalyzersLocks.index_1"
	index, err := core.NewIndex(name, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })

	lock.Lock()
	done := make(chan int)
	go func() {
		c, w := utils.NewContext()
		utils.SetRequestParams(c, map[string]string{"target": name})
		ReloadSearchAnalyzers(c)
		done <- w.Code
	}()
	select {
	case <-done:
		lock.Unlock()
		t.Fatal("reload ran while the synonyms lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	lock.Unlock()
	select {
	case code := <-done:
		assert.Equal(t, http.StatusOK, code)
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not finish after the lock was released")
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

	w, resp := call(t, PutRule, map[string]string{"set_id": set, "rule_id": "r1"}, nil, `{"synonyms":"a, b"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, errorTypeIllegal, errorType(resp))
	stored, err := metadata.Synonyms.Get(set)
	require.NoError(t, err)
	assert.Empty(t, stored.Rules, "a rule breaking an index must not be stored")

	t.Run("filter unused by analyzers is not reloaded", func(t *testing.T) {
		other := "TestSynonymsReloadFailure.set_2"
		cleanupSet(t, other)
		_, _ = call(t, PutSet, map[string]string{"set_id": other}, nil, `{"synonyms_set":[]}`)
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

func TestSynonymsBuildBeforeSave(t *testing.T) {
	set := "TestSynonymsBuildBeforeSave.set_1"
	name := "TestSynonymsBuildBeforeSave.index_1"
	cleanupSet(t, set)
	w, _ := call(t, PutSet, map[string]string{"set_id": set}, nil,
		`{"synonyms_set":[{"id":"r1","synonyms":"s(1,1,'abstain',v,1,0)."},{"id":"r2","synonyms":"s(1,2,'refrain',v,1,0)."}]}`)
	require.Equal(t, http.StatusCreated, w.Code)
	index := newSynonymsIndex(t, name, set, map[string]interface{}{
		"syn": map[string]interface{}{"type": "synonym", "synonyms_set": set, "format": "wordnet"},
	})
	built, err := zedanalysis.RequestAnalyzer(index.GetSettings().Analysis)
	require.NoError(t, err)
	require.NoError(t, index.SetAnalyzers(built))
	require.Equal(t, []string{"refrain", "abstain"}, terms(index, "refrain"))
	stored, err := metadata.Synonyms.Get(set)
	require.NoError(t, err)
	wantRules := stored.Rules

	invalid := []struct {
		name   string
		h      zhttp.HandlerFunc
		params map[string]string
		body   string
	}{
		{name: "put solr rule", h: PutRule, params: map[string]string{"set_id": set, "rule_id": "r3"}, body: `{"synonyms":"a, b"}`},
		{name: "replace with solr rule", h: PutRule, params: map[string]string{"set_id": set, "rule_id": "r1"}, body: `{"synonyms":"x => y"}`},
		{name: "put solr set", h: PutSet, params: map[string]string{"set_id": set}, body: `{"synonyms_set":[{"id":"r1","synonyms":"a, b"}]}`},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			w, resp := call(t, tt.h, tt.params, nil, tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, errorTypeIllegal, errorType(resp))
			assert.Contains(t, resp["error"].(map[string]interface{})["reason"], name)
			stored, err := metadata.Synonyms.Get(set)
			require.NoError(t, err)
			assert.Equal(t, wantRules, stored.Rules)
			assert.Equal(t, []string{"refrain", "abstain"}, terms(index, "refrain"))
		})
	}

	t.Run("valid rule", func(t *testing.T) {
		w, resp := call(t, PutRule, map[string]string{"set_id": set, "rule_id": "r3"}, nil, `{"synonyms":"s(1,3,'forbear',v,1,0)."}`)
		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, map[string]interface{}{
			"_shards": map[string]interface{}{"total": float64(1), "successful": float64(1), "failed": float64(0)},
			"reload_details": []interface{}{map[string]interface{}{
				"index":              name,
				"reloaded_analyzers": []interface{}{"a"},
				"reloaded_node_ids":  []interface{}{},
			}},
		}, resp["reload_analyzers_details"])
		assert.Equal(t, []string{"forbear", "abstain", "refrain"}, terms(index, "forbear"))
		stored, err := metadata.Synonyms.Get(set)
		require.NoError(t, err)
		assert.Len(t, stored.Rules, 3)
	})
	t.Run("delete rule", func(t *testing.T) {
		w, _ := call(t, DeleteRule, map[string]string{"set_id": set, "rule_id": "r1"}, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, []string{"forbear", "refrain"}, terms(index, "forbear"))
	})
	t.Run("index settings keep the set reference", func(t *testing.T) {
		syn := index.GetSettings().Analysis.TokenFilter["syn"].(map[string]interface{})
		assert.Equal(t, set, syn["synonyms_set"])
		assert.NotContains(t, syn, "synonyms")
	})
}

func TestWithRules(t *testing.T) {
	cleanupSet(t, "TestWithRules.other")
	require.NoError(t, metadata.Synonyms.Set("TestWithRules.other", meta.SynonymSet{ID: "TestWithRules.other"}))
	syn := map[string]interface{}{"type": "synonym", "synonyms_set": "TestWithRules.set", "synonyms": []interface{}{"p, q"}}
	other := map[string]interface{}{"type": "synonym", "synonyms_set": "TestWithRules.other"}
	tests := []struct {
		name     string
		analysis *meta.IndexAnalysis
	}{
		{name: "token_filter", analysis: &meta.IndexAnalysis{
			TokenFilter: map[string]interface{}{"syn": syn, "other": other},
			Analyzer:    map[string]*meta.Analyzer{"a": {Tokenizer: "standard", TokenFilter: []string{"syn", "other"}}},
		}},
		{name: "filter alias", analysis: &meta.IndexAnalysis{
			Filter:   map[string]interface{}{"syn": syn, "other": other},
			Analyzer: map[string]*meta.Analyzer{"a": {Tokenizer: "standard", Filter: []string{"syn", "other"}}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, err := json.Marshal(tt.analysis)
			require.NoError(t, err)
			clone := withRules(tt.analysis, "TestWithRules.set", []string{"x, y"})
			filters := clone.TokenFilter
			if tt.analysis.Filter != nil {
				filters = clone.Filter
			}
			assert.Equal(t, map[string]interface{}{"type": "synonym", "synonyms": []string{"p, q", "x, y"}}, filters["syn"])
			assert.Equal(t, other, filters["other"])

			built, err := zedanalysis.RequestAnalyzer(clone)
			require.NoError(t, err)
			assert.Contains(t, built, "a")
			after, err := json.Marshal(tt.analysis)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "the index analysis must not change")
		})
	}
}
