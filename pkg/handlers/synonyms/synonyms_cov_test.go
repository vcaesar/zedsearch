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
	"errors"
	"net/http"
	"testing"
	_ "unsafe" // go:linkname

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
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

func TestSynonyms_StorageErrors(t *testing.T) {
	id := "TestSynonyms_StorageErrors.set_1"
	cleanupSet(t, id)
	require.NoError(t, metadata.Synonyms.Set(id, meta.SynonymSet{ID: id, Rules: []meta.SynonymRule{{ID: "r1", Synonyms: "a, b"}}}))

	params := map[string]string{"set_id": id}
	ruleParams := map[string]string{"set_id": id, "rule_id": "r1"}
	missing := map[string]string{"set_id": "TestSynonyms_StorageErrors.missing"}
	tests := []struct {
		name    string
		storage *failStorage
		h       zhttp.HandlerFunc
		params  map[string]string
		body    string
	}{
		{"list sets", &failStorage{list: errMock}, ListSets, nil, ""},
		{"get set", &failStorage{get: errMock}, GetSet, params, ""},
		{"put set get", &failStorage{get: errMock}, PutSet, params, `{"synonyms_set":[]}`},
		{"put set set", &failStorage{set: errMock}, PutSet, missing, `{"synonyms_set":[]}`},
		{"delete set get", &failStorage{get: errMock}, DeleteSet, params, ""},
		{"delete set delete", &failStorage{del: errMock}, DeleteSet, params, ""},
		{"get rule", &failStorage{get: errMock}, GetRule, ruleParams, ""},
		{"put rule", &failStorage{get: errMock}, PutRule, ruleParams, `{"synonyms":"c, d"}`},
		{"delete rule", &failStorage{get: errMock}, DeleteRule, ruleParams, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFailStorage(t, tt.storage)
			w, resp := call(t, tt.h, tt.params, nil, tt.body)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Equal(t, zederrors.ErrorTypeRuntimeException, errorType(resp))
			assert.Equal(t, errMock.Error(), resp["error"].(map[string]interface{})["reason"])
		})
	}

	set, err := metadata.Synonyms.Get(id)
	require.NoError(t, err, "failed changes keep the stored set")
	assert.Equal(t, []meta.SynonymRule{{ID: "r1", Synonyms: "a, b"}}, set.Rules)

	other := "TestSynonyms_StorageErrors.set_2"
	cleanupSet(t, other)
	require.NoError(t, metadata.Synonyms.Set(other, meta.SynonymSet{ID: other}))
	w, resp := call(t, ListSets, nil, map[string]string{"size": "1000"}, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.GreaterOrEqual(t, resp["count"], float64(2))
}

func TestSynonyms_ReloadInstallFailure(t *testing.T) {
	set := "TestSynonyms_ReloadInstallFailure.set_1"
	name := "TestSynonyms_ReloadInstallFailure.index_1"
	cleanupSet(t, set)
	_, _ = call(t, PutSet, map[string]string{"set_id": set}, nil, `{"synonyms_set":[]}`)
	newSynonymsIndex(t, name, set, map[string]interface{}{"bad": map[string]interface{}{"type": "nope"}})

	w, resp := call(t, ReloadSearchAnalyzers, map[string]string{"target": name}, nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, map[string]interface{}{"total": float64(1), "successful": float64(0), "failed": float64(1)}, resp["_shards"])
	assert.Equal(t, []interface{}{}, resp["reload_details"])
}

func TestSynonyms_IndexWithoutSettings(t *testing.T) {
	set := "TestSynonyms_IndexWithoutSettings.set_1"
	name := "TestSynonyms_IndexWithoutSettings.index_1"
	cleanupSet(t, set)
	index, err := core.NewIndex(name, "disk", 1)
	require.NoError(t, err)
	require.Nil(t, index.GetSettings())
	assert.Nil(t, analysisOf(index), "nil settings")
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })
	assert.Nil(t, analysisOf(index), "settings without analysis")

	w, resp := call(t, PutSet, map[string]string{"set_id": set}, nil, `{"synonyms_set":[{"id":"r1","synonyms":"a, b"}]}`)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, []interface{}{}, resp["reload_analyzers_details"].(map[string]interface{})["reload_details"])
}
