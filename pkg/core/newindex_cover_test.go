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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

// longIndexName is a valid index name too long to be a key of the bolt and
// badger metadata stores, so storing its record fails.
var longIndexName = "TestStoreIndex_MetadataRejected." + strings.Repeat("a", 70000)

func skipWithoutKeyLimit(t *testing.T) {
	t.Helper()
	if config.Global.Cluster.Enable || strings.EqualFold(config.Global.ServerMode, "cluster") ||
		strings.EqualFold(config.Global.MetadataStorage, "etcd") {
		t.Skip("etcd accepts long keys")
	}
}

func TestStoreIndex_MetadataRejected(t *testing.T) {
	skipWithoutKeyLimit(t)
	index, err := NewIndex(longIndexName, StorageTypeDisk, 1)
	require.NoError(t, err)

	var storageErr *StorageError
	t.Run("store", func(t *testing.T) {
		assert.ErrorAs(t, StoreIndex(index), &storageErr)
		_, _, err := GetOrCreateIndex(longIndexName, StorageTypeDisk, 1)
		assert.ErrorAs(t, err, &storageErr)
		_, ok := GetIndex(longIndexName)
		assert.False(t, ok, "an index that was not stored is not cached")
	})

	t.Run("new second shard", func(t *testing.T) {
		assert.Error(t, anyShard(index).NewShard())
	})

	t.Run("mappings update", func(t *testing.T) {
		_, err := anyShard(index).CheckDocument("1", map[string]interface{}{"count": 1.0}, false, 0)
		assert.ErrorAs(t, err, &storageErr)
	})

	t.Run("location is reverted", func(t *testing.T) {
		assert.Error(t, index.setLocation(StorageTypeS3, TierWarm))
		assert.Equal(t, StorageTypeDisk, index.GetStorageType())
		assert.Equal(t, TierHot, index.GetTier())
	})

	t.Run("tier move", func(t *testing.T) {
		useObjectStore(t)
		checkIndex(index) // Settings is set by StoreIndex in the "store" subtest
		index.lock.Lock()
		index.ref.StorageType = StorageTypeS3
		index.ref.Settings.Tier = TierCold
		index.lock.Unlock()
		index.applyTier(TierCold)
		ZED_INDEX_LIST.Add(index)
		t.Cleanup(func() { ZED_INDEX_LIST.Delete(longIndexName) })

		assert.Error(t, MoveIndexTier(longIndexName, TierWarm))
		assert.Equal(t, TierCold, index.GetTier())
		assert.True(t, index.IsReadOnly())
	})

	t.Run("template", func(t *testing.T) {
		assert.Error(t, NewTemplate(longIndexName, &meta.IndexTemplate{IndexPatterns: []string{"TestStoreIndex_MetadataRejected.none-*"}}))
	})
}
