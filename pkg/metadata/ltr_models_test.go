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

package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestLTRModels(t *testing.T) {
	// fixture stores a model owned by the calling subtest, so each runs alone.
	fixture := func(t *testing.T, id string) meta.LTRModel {
		t.Helper()
		m := meta.LTRModel{
			ModelID:     id,
			Description: "test model",
			CreateTime:  1700000000000,
			InferenceConfig: map[string]interface{}{"learning_to_rank": map[string]interface{}{
				"feature_extractors": []interface{}{map[string]interface{}{"query_extractor": map[string]interface{}{
					"feature_name": "title",
					"query":        map[string]interface{}{"match": map[string]interface{}{"title": "{{query}}"}},
				}}},
			}},
			Definition: map[string]interface{}{"linear": map[string]interface{}{"weights": []interface{}{2.5}, "bias": -1.0}},
		}
		require.NoError(t, LTRModels.Set(id, m))
		t.Cleanup(func() { assert.NoError(t, LTRModels.Delete(id)) })
		return m
	}

	t.Run("get", func(t *testing.T) {
		m := fixture(t, "TestLTRModels.model_get")
		got, err := LTRModels.Get(m.ModelID)
		require.NoError(t, err)
		assert.Equal(t, m, *got, "nested definitions round trip as JSON")
	})
	t.Run("set overwrites", func(t *testing.T) {
		m := fixture(t, "TestLTRModels.model_set")
		m.Description = "changed"
		require.NoError(t, LTRModels.Set(m.ModelID, m))
		got, err := LTRModels.Get(m.ModelID)
		require.NoError(t, err)
		assert.Equal(t, "changed", got.Description)
	})
	t.Run("list", func(t *testing.T) {
		a := fixture(t, "TestLTRModels.model_list_a")
		b := fixture(t, "TestLTRModels.model_list_b")
		got, err := LTRModels.List(0, 0)
		require.NoError(t, err)
		byID := make(map[string]*meta.LTRModel, len(got))
		for _, m := range got {
			byID[m.ModelID] = m
		}
		require.Contains(t, byID, a.ModelID)
		require.Contains(t, byID, b.ModelID)
		assert.Equal(t, a, *byID[a.ModelID])
	})
	t.Run("list only models", func(t *testing.T) {
		id := "TestLTRModels.other_kind"
		require.NoError(t, QueryRules.Set(id, meta.QueryRuleset{ID: id}))
		t.Cleanup(func() { assert.NoError(t, QueryRules.Delete(id)) })
		got, err := LTRModels.List(0, 0)
		require.NoError(t, err)
		for _, m := range got {
			assert.NotEqual(t, id, m.ModelID)
		}
	})
	t.Run("get missing", func(t *testing.T) {
		_, err := LTRModels.Get("TestLTRModels.missing")
		assert.ErrorIs(t, err, errors.ErrKeyNotFound)
	})
	t.Run("delete", func(t *testing.T) {
		id := "TestLTRModels.model_delete"
		require.NoError(t, LTRModels.Set(id, meta.LTRModel{ModelID: id}))
		require.NoError(t, LTRModels.Delete(id))
		_, err := LTRModels.Get(id)
		assert.ErrorIs(t, err, errors.ErrKeyNotFound)
	})
	t.Run("get invalid json", func(t *testing.T) {
		key := "TestLTRModels.invalid"
		require.NoError(t, db.Set(LTRModels.key(key), []byte("{")))
		t.Cleanup(func() { assert.NoError(t, LTRModels.Delete(key)) })
		_, err := LTRModels.Get(key)
		assert.Error(t, err)
		_, err = LTRModels.List(0, 0)
		assert.Error(t, err)
	})
	t.Run("set unsupported value", func(t *testing.T) {
		key := "TestLTRModels.unsupported"
		m := meta.LTRModel{ModelID: key, Definition: map[string]interface{}{"linear": make(chan int)}}
		assert.Error(t, LTRModels.Set(key, m))
		_, err := LTRModels.Get(key)
		assert.ErrorIs(t, err, errors.ErrKeyNotFound, "nothing stored")
	})
}

func TestLTRModels_Key(t *testing.T) {
	assert.Equal(t, "/ltr_models/", LTRModels.key(""))
	assert.Equal(t, "/ltr_models/m1", LTRModels.key("m1"))
}

func TestLTRModelsStorageError(t *testing.T) {
	m := useMock(t)
	m.err = errMock

	_, err := LTRModels.List(0, 0)
	assert.ErrorIs(t, err, errMock)
	_, err = LTRModels.Get("TestLTRModelsStorageError.model")
	assert.ErrorIs(t, err, errMock)
	assert.ErrorIs(t, LTRModels.Set("TestLTRModelsStorageError.model", meta.LTRModel{}), errMock)
	assert.ErrorIs(t, LTRModels.Delete("TestLTRModelsStorageError.model"), errMock)
}
