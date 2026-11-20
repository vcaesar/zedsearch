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

package rules

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

type obj = map[string]interface{}

func call(t *testing.T, h zhttp.HandlerFunc, params, query map[string]string, body string) (*httptest.ResponseRecorder, obj) {
	t.Helper()
	c, w := utils.NewContext()
	utils.SetRequestData(c, body)
	utils.SetRequestURL(c, "/es/_query_rules", query)
	utils.SetRequestParams(c, params)
	h(c)
	var resp obj
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	return w, resp
}

func errorType(resp obj) interface{} {
	e, _ := resp["error"].(obj)
	return e["type"]
}

const pinRule = `{"rule_id":"pin","type":"pinned","criteria":[{"type":"exact","metadata":"q","values":["pugs"]}],"actions":{"ids":["1"]},"priority":2}`
const dropRule = `{"rule_id":"drop","type":"exclude","criteria":[{"type":"always"}],"actions":{"docs":[{"_index":"i","_id":"2"}]},"priority":1}`

func TestRulesetCRUD(t *testing.T) {
	id := "TestRulesetCRUD.rs_1"
	t.Cleanup(func() {
		if err := metadata.QueryRules.Delete(id); err != nil {
			t.Log(err)
		}
	})
	params := map[string]string{"ruleset_id": id}

	w, resp := call(t, PutRuleset, params, nil, `{"rules":[`+pinRule+`,`+dropRule+`]}`)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "created", resp["result"])

	w, resp = call(t, PutRuleset, params, nil, `{"rules":[`+pinRule+`,`+dropRule+`]}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "updated", resp["result"])

	w, resp = call(t, GetRuleset, params, nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, id, resp["ruleset_id"])
	assert.Len(t, resp["rules"], 2)

	w, resp = call(t, ListRulesets, nil, map[string]string{"size": "1000"}, "")
	assert.Equal(t, http.StatusOK, w.Code)
	var found obj
	for _, r := range resp["results"].([]interface{}) {
		if r.(obj)["ruleset_id"] == id {
			found = r.(obj)
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, float64(2), found["rule_total_count"])
	assert.Equal(t, obj{"pinned": 1.0, "exclude": 1.0}, found["rule_type_counts"])
	assert.Equal(t, obj{"exact": 1.0, "always": 1.0}, found["rule_criteria_types_counts"])

	w, resp = call(t, TestRuleset, params, nil, `{"match_criteria":{"q":"pugs"}}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, float64(2), resp["total_matched_rules"])
	assert.Equal(t, "drop", resp["matched_rules"].([]interface{})[0].(obj)["rule_id"], "lower priority first")

	ruleParams := map[string]string{"ruleset_id": id, "rule_id": "pin"}
	w, resp = call(t, GetRule, ruleParams, nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pinned", resp["type"])

	w, _ = call(t, PutRule, ruleParams, nil, `{"type":"pinned","criteria":[{"type":"prefix","metadata":"q","values":["pu"]}],"actions":{"ids":["3"]}}`)
	assert.Equal(t, http.StatusOK, w.Code)
	newRule := map[string]string{"ruleset_id": id, "rule_id": "new"}
	w, _ = call(t, PutRule, newRule, nil, `{"type":"exclude","criteria":[{"type":"always"}],"actions":{"ids":["4"]}}`)
	assert.Equal(t, http.StatusCreated, w.Code)

	for _, rid := range []string{"pin", "new", "drop"} {
		w, resp = call(t, DeleteRule, map[string]string{"ruleset_id": id, "rule_id": rid}, nil, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, true, resp["acknowledged"])
	}
	w, resp = call(t, GetRuleset, params, nil, "")
	assert.Equal(t, http.StatusNotFound, w.Code, "the last rule deletes the ruleset")
	assert.Equal(t, errorTypeNotFound, errorType(resp))

	w, _ = call(t, PutRule, newRule, nil, `{"type":"exclude","criteria":[{"type":"always"}],"actions":{"ids":["4"]}}`)
	assert.Equal(t, http.StatusCreated, w.Code, "creates the ruleset")
	w, _ = call(t, DeleteRuleset, params, nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRuleset_Errors(t *testing.T) {
	id := "TestRuleset_Errors.rs_1"
	params := map[string]string{"ruleset_id": id}
	ruleParams := map[string]string{"ruleset_id": id, "rule_id": "x"}
	var many string
	for i := 0; i <= MaxRules; i++ {
		if i > 0 {
			many += ","
		}
		many += fmt.Sprintf(`{"rule_id":"r%d","type":"pinned","criteria":[{"type":"always"}],"actions":{"ids":["1"]}}`, i)
	}
	tests := []struct {
		name   string
		h      zhttp.HandlerFunc
		params map[string]string
		query  map[string]string
		body   string
		code   int
	}{
		{"put bad json", PutRuleset, params, nil, `{`, http.StatusBadRequest},
		{"put empty", PutRuleset, params, nil, `{"rules":[]}`, http.StatusBadRequest},
		{"put too many", PutRuleset, params, nil, `{"rules":[` + many + `]}`, http.StatusBadRequest},
		{"put invalid", PutRuleset, params, nil, `{"rules":[{"rule_id":"a","type":"x"}]}`, http.StatusBadRequest},
		{"put null rule", PutRuleset, params, nil, `{"rules":[null]}`, http.StatusBadRequest},
		{"put null among rules", PutRuleset, params, nil, `{"rules":[` + pinRule + `,null]}`, http.StatusBadRequest},
		{"put duplicate", PutRuleset, params, nil, `{"rules":[` + pinRule + `,` + pinRule + `]}`, http.StatusBadRequest},
		{"get missing", GetRuleset, params, nil, "", http.StatusNotFound},
		{"delete missing", DeleteRuleset, params, nil, "", http.StatusNotFound},
		{"get rule missing set", GetRule, ruleParams, nil, "", http.StatusNotFound},
		{"delete rule missing", DeleteRule, ruleParams, nil, "", http.StatusNotFound},
		{"put rule bad json", PutRule, ruleParams, nil, `{`, http.StatusBadRequest},
		{"put rule invalid", PutRule, ruleParams, nil, `{"type":"pinned"}`, http.StatusBadRequest},
		{"test bad json", TestRuleset, params, nil, `{`, http.StatusBadRequest},
		{"test no criteria", TestRuleset, params, nil, `{}`, http.StatusBadRequest},
		{"test missing", TestRuleset, params, nil, `{"match_criteria":{}}`, http.StatusNotFound},
		{"list bad from", ListRulesets, nil, map[string]string{"from": "-1"}, "", http.StatusBadRequest},
		{"list bad size", ListRulesets, nil, map[string]string{"size": "x"}, "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			var resp obj
			require.NotPanics(t, func() { w, resp = call(t, tt.h, tt.params, tt.query, tt.body) })
			assert.Equal(t, tt.code, w.Code, w.Body.String())
			assert.NotNil(t, errorType(resp))
		})
	}

	t.Run("list overflowing page end", func(t *testing.T) {
		// two owned rulesets keep from=1 below the list length, so the page
		// end is computed; the second one sorts after the first
		second := "TestRuleset_Errors.rs_2"
		for _, rs := range []string{id, second} {
			t.Cleanup(func() {
				if err := metadata.QueryRules.Delete(rs); err != nil {
					t.Log(err)
				}
			})
			w, _ := call(t, PutRuleset, map[string]string{"ruleset_id": rs}, nil, `{"rules":[`+pinRule+`]}`)
			require.Equal(t, http.StatusCreated, w.Code)
		}
		var w *httptest.ResponseRecorder
		var resp obj
		require.NotPanics(t, func() {
			w, resp = call(t, ListRulesets, nil, map[string]string{"from": "1", "size": strconv.Itoa(math.MaxInt)}, "")
		})
		assert.Equal(t, http.StatusOK, w.Code)
		var ids []interface{}
		for _, r := range resp["results"].([]interface{}) {
			ids = append(ids, r.(map[string]interface{})["ruleset_id"])
		}
		assert.Contains(t, ids, second)
	})

	t.Run("rule missing in set", func(t *testing.T) {
		t.Cleanup(func() {
			if err := metadata.QueryRules.Delete(id); err != nil {
				t.Log(err)
			}
		})
		w, _ := call(t, PutRuleset, params, nil, `{"rules":[`+pinRule+`]}`)
		require.Equal(t, http.StatusCreated, w.Code)
		w, _ = call(t, GetRule, ruleParams, nil, "")
		assert.Equal(t, http.StatusNotFound, w.Code)

		big := map[string]string{"ruleset_id": id, "rule_id": "extra"}
		w, _ = call(t, PutRuleset, params, nil, `{"rules":[`+many[:len(many)-len(fmt.Sprintf(`,{"rule_id":"r%d","type":"pinned","criteria":[{"type":"always"}],"actions":{"ids":["1"]}}`, MaxRules))]+`]}`)
		require.Equal(t, http.StatusOK, w.Code)
		w, _ = call(t, PutRule, big, nil, `{"type":"pinned","criteria":[{"type":"always"}],"actions":{"ids":["1"]}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "ruleset is full")
	})
}
