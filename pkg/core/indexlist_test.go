/* Copyright 2022 Zinc Labs Inc. and Contributors
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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
)

func TestIndexList_List(t *testing.T) {
	indexName := "TestIndexList_List.index_1"
	index, exist, err := GetOrCreateIndex(indexName, "", 2)
	assert.NoError(t, err)
	assert.False(t, exist)
	assert.NotNil(t, index)

	rs, err := index.GetReaders(0, 0)
	assert.NoError(t, err)
	assert.NotNil(t, rs)

	got1 := ZED_INDEX_LIST.List()
	assert.NotNil(t, got1)

	got2 := ZED_INDEX_LIST.ListName()
	assert.NotNil(t, got2)

	got3 := ZED_INDEX_LIST.ListStat()
	assert.NotNil(t, got3)

	err = DeleteIndex(indexName)
	assert.NoError(t, err)

	err = ZED_INDEX_LIST.GC()
	assert.NoError(t, err)
}

// init runs before a new index is stored or cached: a failing init leaves no
// trace, a successful one is what the metadata holds.
func TestGetOrCreateIndexWith(t *testing.T) {
	name := "TestGetOrCreateIndexWith.index_1"
	t.Cleanup(func() {
		if _, ok := GetIndex(name); ok {
			assert.NoError(t, DeleteIndex(name))
		}
	})
	setLogs := func(index *Index) error {
		return index.SetSettings(&meta.IndexSettings{IndexType: meta.IndexTypeLogs})
	}

	_, _, err := GetOrCreateIndexWith(name, "", 1, func(*Index) error { return assert.AnError })
	require.ErrorIs(t, err, assert.AnError)
	_, ok := GetIndex(name)
	assert.False(t, ok)
	_, err = metadata.Index.Get(name)
	assert.Error(t, err, "not stored")

	index, exists, err := GetOrCreateIndexWith(name, "", 1, setLogs)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, meta.IndexTypeLogs, index.GetIndexType())
	stored, err := metadata.Index.Get(name)
	require.NoError(t, err)
	assert.Equal(t, meta.IndexTypeLogs, stored.Settings.IndexType)

	_, exists, err = GetOrCreateIndexWith(name, "", 1, func(*Index) error { return assert.AnError })
	require.NoError(t, err, "init doesn't run on an existing index")
	assert.True(t, exists)
}

func TestStorageError(t *testing.T) {
	assert.NoError(t, storageError(nil))
	assert.Same(t, ErrIndexReadOnly, storageError(ErrIndexReadOnly), "read-only is permanent")
	err := storageError(assert.AnError)
	var storageErr *StorageError
	require.ErrorAs(t, err, &storageErr)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, assert.AnError.Error(), err.Error())
}
