/* Copyright 2026 AtomAI.
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

package core

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
)

// newSynonymRefIndex returns an uncached, unstored index whose analysis
// references the synonyms set id through filters ("token_filter" or "filter").
func newSynonymRefIndex(t *testing.T, name, id, filters string) *Index {
	t.Helper()
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	checkIndex(index)
	analysis := &meta.IndexAnalysis{}
	if id != "" {
		m := map[string]interface{}{"syn": map[string]interface{}{"type": "synonym", "synonyms_set": id}}
		if filters == "filter" {
			analysis.Filter = m
		} else {
			analysis.TokenFilter = m
		}
	}
	index.ref.Settings.Analysis = analysis
	t.Cleanup(func() { assert.NoError(t, metadata.Index.Delete(name)) })
	return index
}

func putSynonymSet(t *testing.T, id string) {
	t.Helper()
	require.NoError(t, metadata.Synonyms.Set(id, meta.SynonymSet{ID: id, Rules: []meta.SynonymRule{{ID: "r1", Synonyms: "a, b"}}}))
	t.Cleanup(func() { assert.NoError(t, metadata.Synonyms.Delete(id)) })
}

func TestStoreIndexSynonymSets(t *testing.T) {
	set := "TestStoreIndexSynonymSets.set_1"
	putSynonymSet(t, set)

	tests := []struct {
		name    string
		set     string
		filters string
		wantErr string
	}{
		{name: "no reference", set: ""},
		{name: "existing set", set: set},
		{name: "existing set in filter", set: set, filters: "filter"},
		{name: "missing set", set: "TestStoreIndexSynonymSets.missing", wantErr: "synonyms set [TestStoreIndexSynonymSets.missing] not found"},
		{name: "missing set in filter", set: "TestStoreIndexSynonymSets.missing", filters: "filter", wantErr: "synonyms set [TestStoreIndexSynonymSets.missing] not found"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("TestStoreIndexSynonymSets.index_%d", i)
			err := storeIndex(newSynonymRefIndex(t, name, tt.set, tt.filters))
			if tt.wantErr == "" {
				require.NoError(t, err)
				_, err = metadata.Index.Get(name)
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
			_, err = metadata.Index.Get(name)
			assert.Error(t, err, "the index must not be stored")
		})
	}
}

func TestDeleteSynonymSet(t *testing.T) {
	t.Run("used by a persisted, uncached index", func(t *testing.T) {
		set := "TestDeleteSynonymSet.set_1"
		putSynonymSet(t, set)
		name := "TestDeleteSynonymSet.index_1"
		require.NoError(t, storeIndex(newSynonymRefIndex(t, name, set, "filter")))
		_, cached := GetIndex(name)
		require.False(t, cached)

		used, err := DeleteSynonymSet(set)
		require.NoError(t, err)
		assert.Equal(t, []string{name}, used)
		_, err = metadata.Synonyms.Get(set)
		assert.NoError(t, err, "a used set must not be deleted")
	})
	t.Run("used by a cached, unpersisted index", func(t *testing.T) {
		set := "TestDeleteSynonymSet.set_2"
		putSynonymSet(t, set)
		index := newSynonymRefIndex(t, "TestDeleteSynonymSet.index_2", set, "")
		ZED_INDEX_LIST.Add(index)
		t.Cleanup(func() { ZED_INDEX_LIST.Delete(index.GetName()) })

		used, err := DeleteSynonymSet(set)
		require.NoError(t, err)
		assert.Equal(t, []string{index.GetName()}, used)
	})
	t.Run("used by a persisted and cached index is reported once", func(t *testing.T) {
		set := "TestDeleteSynonymSet.set_3"
		putSynonymSet(t, set)
		index := newSynonymRefIndex(t, "TestDeleteSynonymSet.index_3", set, "")
		require.NoError(t, storeIndex(index))
		ZED_INDEX_LIST.Add(index)
		t.Cleanup(func() { ZED_INDEX_LIST.Delete(index.GetName()) })

		used, err := DeleteSynonymSet(set)
		require.NoError(t, err)
		assert.Equal(t, []string{index.GetName()}, used)
	})
	t.Run("unused", func(t *testing.T) {
		set := "TestDeleteSynonymSet.set_4"
		require.NoError(t, metadata.Synonyms.Set(set, meta.SynonymSet{ID: set}))
		used, err := DeleteSynonymSet(set)
		require.NoError(t, err)
		assert.Empty(t, used)
		_, err = metadata.Synonyms.Get(set)
		assert.Error(t, err)
	})
}

// TestDeleteSynonymSetRace checks a racing publication and delete never
// both succeed.
func TestDeleteSynonymSetRace(t *testing.T) {
	for i := 0; i < 20; i++ {
		set := fmt.Sprintf("TestDeleteSynonymSetRace.set_%d", i)
		require.NoError(t, metadata.Synonyms.Set(set, meta.SynonymSet{ID: set}))
		index := newSynonymRefIndex(t, fmt.Sprintf("TestDeleteSynonymSetRace.index_%d", i), set, "")

		var wg sync.WaitGroup
		var storeErr, deleteErr error
		var used []string
		wg.Add(2)
		go func() { defer wg.Done(); storeErr = storeIndex(index) }()
		go func() { defer wg.Done(); used, deleteErr = DeleteSynonymSet(set) }()
		wg.Wait()
		require.NoError(t, deleteErr)

		if len(used) == 0 {
			assert.Error(t, storeErr, "published after the set was deleted")
		} else {
			assert.NoError(t, storeErr)
			assert.NoError(t, metadata.Synonyms.Delete(set))
		}
	}
}
