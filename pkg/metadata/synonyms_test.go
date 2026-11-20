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

func TestSynonyms(t *testing.T) {
	id := "TestSynonyms.set_1"
	set := meta.SynonymSet{ID: id, Rules: []meta.SynonymRule{{ID: "r1", Synonyms: "hello, hi"}}}

	t.Run("set", func(t *testing.T) {
		require.NoError(t, Synonyms.Set(id, set))
	})
	t.Run("get", func(t *testing.T) {
		got, err := Synonyms.Get(id)
		require.NoError(t, err)
		assert.Equal(t, set, *got)
	})
	t.Run("list", func(t *testing.T) {
		got, err := Synonyms.List(0, 0)
		require.NoError(t, err)
		found := false
		for _, s := range got {
			if s.ID == id {
				found = true
				assert.Equal(t, set.Rules, s.Rules)
			}
		}
		assert.True(t, found)
	})
	t.Run("delete", func(t *testing.T) {
		require.NoError(t, Synonyms.Delete(id))
		_, err := Synonyms.Get(id)
		assert.ErrorIs(t, err, errors.ErrKeyNotFound)
	})
	t.Run("get invalid json", func(t *testing.T) {
		key := "TestSynonyms.invalid"
		require.NoError(t, db.Set(Synonyms.key(key), []byte("{")))
		t.Cleanup(func() { assert.NoError(t, Synonyms.Delete(key)) })
		_, err := Synonyms.Get(key)
		assert.Error(t, err)
		_, err = Synonyms.List(0, 0)
		assert.Error(t, err)
	})
}

func TestSynonymsStorageError(t *testing.T) {
	m := useMock(t)
	m.err = errMock

	_, err := Synonyms.List(0, 0)
	assert.ErrorIs(t, err, errMock)
	_, err = Synonyms.Get("TestSynonymsStorageError.set")
	assert.ErrorIs(t, err, errMock)
	assert.ErrorIs(t, Synonyms.Set("TestSynonymsStorageError.set", meta.SynonymSet{}), errMock)
	assert.ErrorIs(t, Synonyms.Delete("TestSynonymsStorageError.set"), errMock)
}
