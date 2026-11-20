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
	"errors"
	"net/http"
	"testing"
	_ "unsafe" // go:linkname

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zederrors "github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/metadata/storage"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

// metadataDB is the unexported storage of pkg/metadata, swapped to inject failures.
//
//go:linkname metadataDB github.com/vcaesar/zedsearch/pkg/metadata.db
var metadataDB storage.Storager

var errMock = errors.New("mock storage failure")

// failStorage wraps the metadata storage, failing the calls whose error is set.
type failStorage struct {
	storage.Storager
	list, get, set, del error
}

func (f *failStorage) List(prefix string, offset, limit int) ([][]byte, error) {
	if f.list != nil {
		return nil, f.list
	}
	return f.Storager.List(prefix, offset, limit)
}

func (f *failStorage) Get(key string) ([]byte, error) {
	if f.get != nil {
		return nil, f.get
	}
	return f.Storager.Get(key)
}

func (f *failStorage) Set(key string, value []byte) error {
	if f.set != nil {
		return f.set
	}
	return f.Storager.Set(key, value)
}

func (f *failStorage) Delete(key string) error {
	if f.del != nil {
		return f.del
	}
	return f.Storager.Delete(key)
}

// useFailStorage swaps the metadata storage for f for one test.
func useFailStorage(t *testing.T, f *failStorage) {
	t.Helper()
	prev := metadataDB
	f.Storager = prev
	metadataDB = f
	t.Cleanup(func() { metadataDB = prev })
}

func TestRuleset_StorageErrors(t *testing.T) {
	id := "TestRuleset_StorageErrors.rs_1"
	one := "TestRuleset_StorageErrors.rs_one"
	rule := &meta.QueryRule{ID: "pin", Type: "pinned",
		Criteria: []meta.QueryRuleCriteria{{Type: "always"}}, Actions: meta.QueryRuleActions{IDs: []string{"1"}}}
	drop := &meta.QueryRule{ID: "drop", Type: "exclude",
		Criteria: []meta.QueryRuleCriteria{{Type: "always"}}, Actions: meta.QueryRuleActions{IDs: []string{"2"}}}
	require.NoError(t, metadata.QueryRules.Set(id, meta.QueryRuleset{ID: id, Rules: []*meta.QueryRule{rule, drop}}))
	require.NoError(t, metadata.QueryRules.Set(one, meta.QueryRuleset{ID: one, Rules: []*meta.QueryRule{rule}}))
	t.Cleanup(func() {
		for _, k := range []string{id, one} {
			if err := metadata.QueryRules.Delete(k); err != nil {
				t.Log(err)
			}
		}
	})

	params := map[string]string{"ruleset_id": id}
	missing := map[string]string{"ruleset_id": "TestRuleset_StorageErrors.missing"}
	tests := []struct {
		name    string
		storage *failStorage
		h       zhttp.HandlerFunc
		params  map[string]string
		body    string
	}{
		{"list", &failStorage{list: errMock}, ListRulesets, nil, ""},
		{"get ruleset", &failStorage{get: errMock}, GetRuleset, params, ""},
		{"put ruleset get", &failStorage{get: errMock}, PutRuleset, params, `{"rules":[` + pinRule + `]}`},
		{"put ruleset set", &failStorage{set: errMock}, PutRuleset, missing, `{"rules":[` + pinRule + `]}`},
		{"delete ruleset", &failStorage{del: errMock}, DeleteRuleset, params, ""},
		{"put rule get", &failStorage{get: errMock}, PutRule, map[string]string{"ruleset_id": id, "rule_id": "x"},
			`{"type":"exclude","criteria":[{"type":"always"}],"actions":{"ids":["4"]}}`},
		{"delete rule set", &failStorage{set: errMock}, DeleteRule, map[string]string{"ruleset_id": id, "rule_id": "pin"}, ""},
		{"delete last rule", &failStorage{del: errMock}, DeleteRule, map[string]string{"ruleset_id": one, "rule_id": "pin"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFailStorage(t, tt.storage)
			w, resp := call(t, tt.h, tt.params, nil, tt.body)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Equal(t, zederrors.ErrorTypeRuntimeException, errorType(resp))
			assert.Equal(t, errMock.Error(), resp["error"].(obj)["reason"])
		})
	}

	for _, k := range []string{id, one} {
		_, err := metadata.QueryRules.Get(k)
		assert.NoError(t, err, "failed changes keep the stored ruleset")
	}
	w, resp := call(t, ListRulesets, nil, map[string]string{"size": "1000"}, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.GreaterOrEqual(t, resp["count"], float64(2))
}
