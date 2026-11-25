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

package ltr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	_ "unsafe" // go:linkname

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zederrors "github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/metadata/storage"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

type obj = map[string]interface{}

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

func useFailStorage(t *testing.T, f *failStorage) {
	t.Helper()
	prev := metadataDB
	f.Storager = prev
	metadataDB = f
	t.Cleanup(func() { metadataDB = prev })
}

func call(t *testing.T, h zhttp.HandlerFunc, id, body string) (*httptest.ResponseRecorder, obj) {
	t.Helper()
	c, w := utils.NewContext()
	utils.SetRequestData(c, body)
	utils.SetRequestURL(c, "/es/_ml/trained_models", nil)
	if id != "" {
		utils.SetRequestParams(c, map[string]string{"model_id": id})
	}
	h(c)
	var resp obj
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	return w, resp
}

func errorType(resp obj) interface{} {
	e, _ := resp["error"].(obj)
	return e["type"]
}

const model = `{
	"inference_config": {"learning_to_rank": {"feature_extractors": [
		{"query_extractor": {"feature_name": "title", "query": {"match": {"title": "{{q}}"}}}}
	]}},
	"definition": {"linear": {"weights": {"title": 1.5}, "bias": 0.5}}
}`

func TestModelCRUD(t *testing.T) {
	id := "TestModelCRUD.model_1"
	t.Cleanup(func() {
		if err := metadata.LTRModels.Delete(id); err != nil {
			t.Log(err)
		}
	})

	w, resp := call(t, PutModel, id, model)
	require.Equal(t, http.StatusOK, w.Code, resp)
	assert.Equal(t, id, resp["model_id"])
	assert.NotZero(t, resp["create_time"])

	w, resp = call(t, PutModel, id, model)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, errorTypeExists, errorType(resp))

	w, resp = call(t, GetModels, id, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1.0, resp["count"])
	configs := resp["trained_model_configs"].([]interface{})
	assert.Equal(t, id, configs[0].(obj)["model_id"])

	for _, all := range []string{"", "_all", "*"} {
		w, resp = call(t, GetModels, all, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.GreaterOrEqual(t, resp["count"], 1.0)
	}

	w, resp = call(t, DeleteModel, id, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, resp["acknowledged"])

	w, resp = call(t, GetModels, id, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, errorTypeNotFound, errorType(resp))
	w, _ = call(t, DeleteModel, id, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPutModel_Invalid(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"bad json", `{`, ""},
		{"no learning_to_rank", `{"inference_config": {}, "definition": {"linear": {}}}`, "must be an object"},
		{"bad definition", `{"inference_config": {"learning_to_rank": {"feature_extractors": [
			{"query_extractor": {"feature_name": "a", "query": {"match_all": {}}}}]}}, "definition": {"onnx": {}}}`, "unknown definition [onnx]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, resp := call(t, PutModel, "TestPutModel_Invalid.model_1", tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, zederrors.ErrorTypeParsingException, errorType(resp))
			assert.Contains(t, resp["error"].(obj)["reason"], tt.want)
		})
	}
	_, err := metadata.LTRModels.Get("TestPutModel_Invalid.model_1")
	assert.ErrorIs(t, err, zederrors.ErrKeyNotFound, "an invalid model is not stored")
}

func TestModel_StorageErrors(t *testing.T) {
	id := "TestModel_StorageErrors.model_1"
	w, _ := call(t, PutModel, id, model)
	require.Equal(t, http.StatusOK, w.Code)
	t.Cleanup(func() {
		if err := metadata.LTRModels.Delete(id); err != nil {
			t.Log(err)
		}
	})
	missing := "TestModel_StorageErrors.missing"

	tests := []struct {
		name    string
		storage *failStorage
		h       zhttp.HandlerFunc
		id      string
		body    string
	}{
		{"list", &failStorage{list: errMock}, GetModels, "", ""},
		{"get", &failStorage{get: errMock}, GetModels, id, ""},
		{"put get", &failStorage{get: errMock}, PutModel, missing, model},
		{"put set", &failStorage{set: errMock}, PutModel, missing, model},
		{"delete", &failStorage{del: errMock}, DeleteModel, id, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFailStorage(t, tt.storage)
			w, resp := call(t, tt.h, tt.id, tt.body)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Equal(t, zederrors.ErrorTypeRuntimeException, errorType(resp))
		})
	}
	_, err := metadata.LTRModels.Get(id)
	assert.NoError(t, err, "a failed delete keeps the model")
}
