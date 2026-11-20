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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot/analysis/analyzer"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
)

func TestIndexList_ListMap(t *testing.T) {
	name := "TestIndexList_ListMap.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	indexes := ZED_INDEX_LIST.ListMap()
	assert.Same(t, index, indexes[name])
	assert.Len(t, indexes, ZED_INDEX_LIST.Len())
}

func TestIndexList_GetOrCreateInvalidName(t *testing.T) {
	_, _, err := GetOrCreateIndex("_invalid", StorageTypeDisk, 1)
	assert.Error(t, err)
}

func TestIndexList_DeleteCloseFails(t *testing.T) {
	name := "TestIndexList_DeleteCloseFails.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() {
		assert.NoError(t, metadata.Index.Delete(name))
		assert.NoError(t, os.RemoveAll(filepath.Join(config.Global.DataPath, name)))
	})
	shard := anyShard(index)
	require.NoError(t, shard.OpenWAL())
	ZED_INDEX_SHARD_WAL_LIST.Remove(shard.GetShardName())
	require.NoError(t, shard.wal.Close())

	ZED_INDEX_LIST.Delete(name)
	_, ok := GetIndex(name)
	assert.False(t, ok, "the index is dropped even when closing it fails")
}

func TestLoadZedIndexesFromMetadataCases(t *testing.T) {
	record := func(t *testing.T, name, version string) []byte {
		t.Helper()
		index, err := NewIndex(name, StorageTypeDisk, 1)
		require.NoError(t, err)
		index.ref.Version = version
		data, err := index.MarshalJSON()
		require.NoError(t, err)
		return data
	}
	reload := func(t *testing.T) error {
		t.Helper()
		require.NoError(t, ZED_INDEX_LIST.Close())
		return LoadZedIndexesFromMetadata(meta.Version)
	}

	t.Run("upgraded record cannot be stored", func(t *testing.T) {
		skipWithoutKeyLimit(t)
		key := "TestLoadZedIndexesFromMetadataCases.index_5"
		// the record is stored again under its name, too long for a key
		require.NoError(t, metadata.Index.Set(key, record(t, longIndexName, "v0.2.9")))
		t.Cleanup(func() { assert.NoError(t, metadata.Index.Delete(key)) })
		assert.Error(t, reload(t))
	})

	t.Run("upgrade an old index", func(t *testing.T) {
		name := "TestLoadZedIndexesFromMetadataCases.index_1"
		require.NoError(t, metadata.Index.Set(name, record(t, name, "v0.2.9")))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
		require.NoError(t, reload(t))
		stored, err := metadata.Index.Get(name)
		require.NoError(t, err)
		assert.Equal(t, meta.Version, stored.Version)
		_, ok := GetIndex(name)
		assert.True(t, ok)
	})

	t.Run("upgrade fails", func(t *testing.T) {
		name := "TestLoadZedIndexesFromMetadataCases.index_2"
		// the upgrade moves the index directory aside, onto a directory in the way
		dir := filepath.Join(config.Global.DataPath, name)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(dir+"_old", "data"), 0o750))
		require.NoError(t, metadata.Index.Set(name, record(t, name, "v0.2.4")))
		t.Cleanup(func() {
			assert.NoError(t, metadata.Index.Delete(name))
			assert.NoError(t, os.RemoveAll(dir))
			assert.NoError(t, os.RemoveAll(dir+"_old"))
		})
		assert.Error(t, reload(t))
	})

	t.Run("invalid analysis", func(t *testing.T) {
		name := "TestLoadZedIndexesFromMetadataCases.index_3"
		require.NoError(t, metadata.Index.Set(name, invalidAnalysisMeta(t, name)))
		t.Cleanup(func() { assert.NoError(t, metadata.Index.Delete(name)) })
		assert.Error(t, reload(t))
	})

	t.Run("invalid record", func(t *testing.T) {
		name := "TestLoadZedIndexesFromMetadataCases.index_4"
		require.NoError(t, metadata.Index.Set(name, []byte("not json")))
		t.Cleanup(func() { assert.NoError(t, metadata.Index.Delete(name)) })
		assert.Error(t, reload(t))
	})

	require.NoError(t, reload(t))
}

func TestDeleteIndex_ObjectStorageFails(t *testing.T) {
	store := useObjectStore(t)
	for _, storageType := range []string{StorageTypeS3, StorageTypeGCS, StorageTypeOSS} {
		t.Run(storageType, func(t *testing.T) {
			name := "TestDeleteIndex_ObjectStorageFails.index_" + storageType
			index, err := NewIndex(name, StorageTypeDisk, 1)
			require.NoError(t, err)
			require.NoError(t, StoreIndex(index))
			index.lock.Lock()
			index.ref.StorageType = storageType
			index.lock.Unlock()
			store.SetErr(assert.AnError)
			defer store.SetErr(nil)
			assert.NoError(t, DeleteIndex(name), "the object storage cleanup is best effort")
			_, ok := GetIndex(name)
			assert.False(t, ok)
		})
	}
}

func TestNewIndexCases(t *testing.T) {
	t.Run("invalid characters", func(t *testing.T) {
		_, err := NewIndex("bad/name", StorageTypeDisk, 1)
		assert.Error(t, err)
	})

	t.Run("default shard number", func(t *testing.T) {
		index, err := NewIndex("TestNewIndexCases.index_1", "", 0)
		require.NoError(t, err)
		assert.Equal(t, config.Global.Shard.Num, index.GetShardNum())
	})

	t.Run("too many shards", func(t *testing.T) {
		_, err := NewIndex("TestNewIndexCases.index_2", StorageTypeDisk, 1025)
		assert.Error(t, err)
	})
}

func TestOpenIndexReaderCases(t *testing.T) {
	t.Run("storage not configured", func(t *testing.T) {
		old := config.Global.S3.Bucket
		config.Global.S3.Bucket = ""
		defer func() { config.Global.S3.Bucket = old }()
		_, err := OpenIndexReader("TestOpenIndexReaderCases.index_1", StorageTypeS3, nil)
		assert.Error(t, err)
	})

	t.Run("no data on disk", func(t *testing.T) {
		name := "TestOpenIndexReaderCases.index_2"
		t.Cleanup(func() { assert.NoError(t, os.RemoveAll(filepath.Join(config.Global.DataPath, name))) })
		_, err := OpenIndexReader(name, StorageTypeDisk, analyzer.NewStandardAnalyzer())
		assert.Error(t, err)
	})
}

func TestLoadTemplateMissing(t *testing.T) {
	tpl, ok, err := LoadTemplate("TestLoadTemplateMissing.tpl_1")
	assert.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, tpl)
}

func TestAliasList_GetAliasMapFiltersIndexes(t *testing.T) {
	al := &AliasList{Aliases: map[string][]string{"alias": {"index_1", "index_2"}}}
	got := al.GetAliasMap([]string{"index_1"}, nil)
	assert.Equal(t, M{"index_1": M{"aliases": M{"alias": struct{}{}}}}, got)
}

// waitBlocked waits until n goroutines running fn are blocked with the wait
// reason state, such as "sync.RWMutex.Lock".
func waitBlocked(t *testing.T, fn, state string, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		buf := make([]byte, 1<<20)
		for {
			if size := runtime.Stack(buf, true); size < len(buf) {
				buf = buf[:size]
				break
			}
			buf = make([]byte, 2*len(buf))
		}
		found := 0
		for _, g := range strings.Split(string(buf), "\n\n") {
			header, _, _ := strings.Cut(g, "\n")
			if strings.Contains(header, "["+state) && strings.Contains(g, fn) {
				found++
			}
		}
		return found >= n
	}, 10*time.Second, time.Millisecond)
}

// Two requests creating the same index at once get the same index.
func TestIndexList_GetOrCreateConcurrently(t *testing.T) {
	name := "TestIndexList_GetOrCreateConcurrently.index_1"
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	type result struct {
		index  *Index
		exists bool
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	// both look the index up before either creates it
	func() {
		ZED_INDEX_LIST.lock.Lock()
		defer ZED_INDEX_LIST.lock.Unlock()
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				index, exists, err := GetOrCreateIndex(name, StorageTypeDisk, 1)
				results <- result{index, exists, err}
			}()
		}
		waitBlocked(t, "core.(*IndexList).GetOrCreate(", "sync.RWMutex.RLock", 2)
	}()
	wg.Wait()
	close(results)

	var got []result
	for r := range results {
		require.NoError(t, r.err)
		got = append(got, r)
	}
	require.Len(t, got, 2)
	assert.Same(t, got[0].index, got[1].index)
	assert.NotEqual(t, got[0].exists, got[1].exists, "one created it, the other found it")
}

// Two requests loading an index created on another node get the same index.
func TestLoadIndex_Concurrently(t *testing.T) {
	name := "TestLoadIndex_Concurrently.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	loaded := make(chan *Index, 2)
	var wg sync.WaitGroup
	func() {
		ZED_INDEX_LIST.lock.Lock()
		defer ZED_INDEX_LIST.lock.Unlock()
		delete(ZED_INDEX_LIST.Indexes, name) // stored, not loaded on this node
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				index, err := LoadIndex(name)
				assert.NoError(t, err)
				loaded <- index
			}()
		}
		waitBlocked(t, "core.LoadIndex(", "sync.RWMutex.RLock", 2)
	}()
	wg.Wait()
	close(loaded)

	first, second := <-loaded, <-loaded
	require.NotNil(t, first)
	assert.Same(t, first, second)
	assert.NotSame(t, index, first, "loaded from the metadata store")
}
