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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// failWritesAfter makes WriteGuard reject every write after the first n.
func failWritesAfter(n int32) {
	var calls atomic.Int32
	WriteGuard = func(string) error {
		if calls.Add(1) > n {
			return assert.AnError
		}
		return nil
	}
}

// waitDocument waits until docID of index is searchable.
func waitDocument(t *testing.T, index *Index, docID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := index.GetDocument(docID)
		return err == nil
	}, 30*time.Second, 20*time.Millisecond, "document [%s] was not applied from the WAL", docID)
}

// blockShardDir puts a file where the directory of the shard goes, so its
// WAL cannot be opened.
func blockShardDir(t *testing.T, index *Index, shard *IndexShard) {
	t.Helper()
	dir := filepath.Join(config.Global.DataPath, index.GetName())
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, shard.GetID()), nil, 0o600))
}

// otherShard returns a first layer shard of the index other than shard.
func otherShard(index *Index, shard *IndexShard) *IndexShard {
	for _, other := range index.shards {
		if other != shard {
			return other
		}
	}
	return nil
}

var matchAll = &meta.Query{MatchAll: &meta.MatchAllQuery{}}

func TestIndex_GetIndexAndSettings(t *testing.T) {
	index, err := NewIndex("TestIndex_GetIndexAndSettings.index_1", StorageTypeDisk, 1)
	require.NoError(t, err)
	assert.Equal(t, index.GetName(), index.GetIndex().Name)
	assert.Nil(t, index.defaultSearchAnalyzer(), "an index without analyzers")

	assert.NoError(t, index.SetSettings(nil))
	lifecycle := &meta.IndexLifecycle{WarmAfter: "7d"}
	analysis := func(key string) *meta.IndexAnalysis {
		return &meta.IndexAnalysis{
			CharFilter:  map[string]interface{}{key: map[string]interface{}{"type": "html_strip"}},
			Tokenizer:   map[string]interface{}{key: map[string]interface{}{"type": "standard"}},
			TokenFilter: map[string]interface{}{key: map[string]interface{}{"type": "lowercase"}},
		}
	}
	require.NoError(t, index.SetSettings(&meta.IndexSettings{Lifecycle: lifecycle, Analysis: analysis("a")}))
	require.NoError(t, index.SetSettings(&meta.IndexSettings{Analysis: analysis("b")}))
	settings := index.GetSettings()
	assert.Equal(t, lifecycle, settings.Lifecycle)
	assert.Len(t, settings.Analysis.CharFilter, 2)
	assert.Len(t, settings.Analysis.Tokenizer, 2)
	assert.Len(t, settings.Analysis.TokenFilter, 2)

	require.NoError(t, index.SetMappings(nil))
	_, ok := index.GetMappings().GetProperty("_id")
	assert.True(t, ok)
}

func TestIndex_SetSettingsAndMappingsErrors(t *testing.T) {
	t.Run("logs index with a text timestamp", func(t *testing.T) {
		index, err := NewIndex("TestIndex_SetSettingsAndMappingsErrors.index_1", StorageTypeDisk, 1)
		require.NoError(t, err)
		index.ref.Mappings = meta.NewMappings()
		index.ref.Mappings.SetProperty(meta.TimeFieldName, meta.NewProperty("text"))
		assert.Error(t, index.SetSettings(&meta.IndexSettings{IndexType: meta.IndexTypeLogs}))
	})

	t.Run("disable _source of a nested index", func(t *testing.T) {
		index, err := NewIndex("TestIndex_SetSettingsAndMappingsErrors.index_2", StorageTypeDisk, 1)
		require.NoError(t, err)
		require.NoError(t, index.SetMappings(nestedIndexMappings(t)))
		mappings := meta.NewMappings()
		mappings.Source = &meta.SourceMapping{Enabled: false}
		assert.Error(t, index.SetMappings(mappings))
	})
}

func TestIndex_UseTemplateErrors(t *testing.T) {
	tests := []struct {
		name     string
		settings *meta.IndexSettings
	}{
		{name: "invalid index type", settings: &meta.IndexSettings{IndexType: "bogus"}},
		{name: "invalid analyzer", settings: &meta.IndexSettings{Analysis: &meta.IndexAnalysis{
			Analyzer: map[string]*meta.Analyzer{"default": {Type: "bogus"}},
		}}},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexName := "TestIndex_UseTemplateErrors.index_" + strconv.Itoa(i)
			tplName := "TestIndex_UseTemplateErrors.tpl_" + strconv.Itoa(i)
			require.NoError(t, NewTemplate(tplName, &meta.IndexTemplate{
				IndexPatterns: []string{indexName},
				Priority:      9000 + i,
				Template:      meta.TemplateTemplate{Settings: tt.settings},
			}))
			t.Cleanup(func() { assert.NoError(t, DeleteTemplate(tplName)) })

			_, err := NewIndex(indexName, StorageTypeDisk, 1)
			assert.Error(t, err)
			_, _, err = GetOrCreateIndex(indexName, StorageTypeDisk, 1)
			assert.Error(t, err)
		})
	}
}

func TestIndex_DocumentJoinAcrossShards(t *testing.T) {
	name := "TestIndex_DocumentJoinAcrossShards.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 2)
	require.NoError(t, err)
	require.NoError(t, index.SetMappings(nestedIndexMappings(t)))
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	parentShard := index.GetShardByDocID("q1")
	childID := ""
	for i := 0; childID == ""; i++ {
		if id := "a" + strconv.Itoa(i); index.GetShardByDocID(id) != parentShard {
			childID = id
		}
	}
	child := func(title string) map[string]interface{} {
		return map[string]interface{}{"title": title, "my_join": map[string]interface{}{"name": "answer", "parent": "q1"}}
	}
	require.NoError(t, index.CreateDocument("q1", map[string]interface{}{"title": "question", "my_join": "question"}, false))
	require.NoError(t, index.CreateDocument(childID, child("answer"), false))
	waitForDocs(t, index, 2)

	hit, err := index.GetDocument(childID)
	require.NoError(t, err, "found on the shard of its parent")
	assert.Equal(t, childID, hit.ID)
	assert.NoError(t, index.UpdateDocument(childID, child("answer two"), false), "found on the shard of its parent")
	assert.ErrorIs(t, index.DeleteDocument("missing"), errors.ErrorIDNotFound)
	assert.NoError(t, index.DeleteDocument(childID))
}

func TestIndex_DocumentOtherShardWALFails(t *testing.T) {
	name := "TestIndex_DocumentOtherShardWALFails.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 2)
	require.NoError(t, err)
	require.NoError(t, index.SetMappings(nestedIndexMappings(t)))
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	blockShardDir(t, index, otherShard(index, index.GetShardByDocID("x")))
	_, err = index.GetDocument("x")
	assert.Error(t, err)
	assert.Error(t, index.DeleteDocument("x"))
	assert.NotErrorIs(t, index.DeleteDocument("x"), errors.ErrorIDNotFound)
}

// A join child is written to the shard of its parent, which may fail to open
// even though the shard of the child id did.
func TestIndex_UpdateDocumentRoutedShardFails(t *testing.T) {
	name := "TestIndex_UpdateDocumentRoutedShardFails.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 2)
	require.NoError(t, err)
	// a join property without the hidden join fields: ids are not looked up on other shards
	m := meta.NewMappings()
	m.SetProperty("my_join", meta.Property{Type: "join", Index: true, Relations: map[string][]string{"question": {"answer"}}})
	index.ref.Mappings = m
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	parentShard := index.GetShardByDocID("p1")
	childID := ""
	for i := 0; childID == ""; i++ {
		if id := "a" + strconv.Itoa(i); index.GetShardByDocID(id) != parentShard {
			childID = id
		}
	}
	blockShardDir(t, index, parentShard)
	err = index.UpdateDocument(childID, map[string]interface{}{"my_join": map[string]interface{}{"name": "answer", "parent": "p1"}}, true)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, errors.ErrorIDNotFound)
}

func TestIndex_NestedWriteFailures(t *testing.T) {
	name := "TestIndex_NestedWriteFailures.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, index.SetMappings(nestedIndexMappings(t)))
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	t.Cleanup(func() { WriteGuard = nil })

	comments := func(n int) map[string]interface{} {
		list := make([]interface{}, 0, n)
		for i := 0; i < n; i++ {
			list = append(list, map[string]interface{}{"name": "c" + strconv.Itoa(i), "stars": float64(i)})
		}
		return map[string]interface{}{"title": "post", "comments": list}
	}

	t.Run("update of a missing document", func(t *testing.T) {
		assert.NoError(t, index.CreateDocument("d0", comments(1), true))
	})

	t.Run("invalid nested document", func(t *testing.T) {
		doc := map[string]interface{}{"title": "post", "comments": []interface{}{map[string]interface{}{"stars": "abc"}}}
		assert.Error(t, index.CreateDocument("d1", doc, false))
	})

	t.Run("nested document write fails", func(t *testing.T) {
		failWritesAfter(1)
		err := index.CreateDocument("d2", comments(2), false)
		WriteGuard = nil
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("stale nested document delete fails", func(t *testing.T) {
		require.NoError(t, index.CreateDocument("d3", comments(2), false))
		waitDocument(t, index, "d3")

		failWritesAfter(2)
		err := index.UpdateDocument("d3", comments(1), false)
		WriteGuard = nil
		assert.ErrorIs(t, err, assert.AnError, "update")

		for _, n := range []int32{0, 1} {
			failWritesAfter(n)
			err := index.DeleteDocument("d3")
			WriteGuard = nil
			assert.ErrorIs(t, err, assert.AnError, "delete after %d writes", n)
		}
	})

	t.Run("stored nested value is not an object", func(t *testing.T) {
		entry, err := json.Marshal(map[string]interface{}{
			meta.IDFieldName:     "d4",
			meta.ActionFieldName: meta.ActionTypeInsert,
			meta.ShardFieldName:  ShardIDNeedLatest,
			meta.SourceFieldName: map[string]interface{}{"comments": []interface{}{1}},
		})
		require.NoError(t, err)
		require.NoError(t, anyShard(index).writeWAL(entry))
		waitDocument(t, index, "d4")
		assert.Error(t, index.DeleteDocument("d4"))
		assert.Error(t, index.UpdateDocument("d4", comments(1), false))
	})

	t.Run("readers fail", func(t *testing.T) {
		index.retired.Store(true)
		defer index.retired.Store(false)
		assert.Error(t, index.CreateDocument("d5", comments(1), true))
		_, err := anyShard(index).FindShardByDocID("d0")
		assert.ErrorIs(t, err, ErrIndexReadOnly)
		_, err = index.GetReaders(0, 0)
		assert.Error(t, err)
		_, err = index.Search(&meta.ZedQuery{Query: matchAll, Size: 10})
		assert.Error(t, err)
		_, err = MultiSearch([]string{name}, &meta.ZedQuery{Query: matchAll, Size: 10})
		assert.Error(t, err)
	})
}

func TestIndexShard_GetReadersTimeRange(t *testing.T) {
	name := "TestIndexShard_GetReadersTimeRange.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	shard := anyShard(index)
	second := shard.shards[0]
	atomic.StoreInt64(&second.ref.Stats.DocTimeMin, 100)
	atomic.StoreInt64(&second.ref.Stats.DocTimeMax, 200)
	tests := []struct {
		name             string
		timeMin, timeMax int64
		want             int
	}{
		{name: "after the shard", timeMin: 300, want: 0},
		{name: "before the shard", timeMax: 50, want: 0},
		{name: "inside the shard", timeMin: 150, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readers, err := shard.GetReaders(tt.timeMin, tt.timeMax)
			require.NoError(t, err)
			assert.Len(t, readers, tt.want)
			for _, r := range readers {
				assert.NoError(t, r.Close())
			}
		})
	}
}

func TestIndexShard_GetReadersPartialFailure(t *testing.T) {
	name := "TestIndexShard_GetReadersPartialFailure.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	shard := anyShard(index)
	require.NoError(t, shard.OpenWAL())
	next := filepath.Join(config.Global.DataPath, filepath.FromSlash(shard.secondShardName(1)))
	require.NoError(t, os.WriteFile(next, nil, 0o600))
	old := config.Global.Shard.MaxSize
	config.Global.Shard.MaxSize = 0
	err = shard.CheckShards()
	config.Global.Shard.MaxSize = old
	require.Error(t, err)
	require.Equal(t, int64(2), shard.GetShardNum())

	_, err = index.GetReaders(0, 0)
	assert.Error(t, err, "the second shard cannot be opened")
}

func TestIndex_ColdWithoutData(t *testing.T) {
	store := useObjectStore(t)
	name := "TestIndex_ColdWithoutData.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	require.NoError(t, index.setLocation(StorageTypeS3, TierCold))
	index.applyTier(TierCold)
	shard := anyShard(index)
	names := index.shardDataNames()

	t.Run("nothing to read", func(t *testing.T) {
		_, err := shard.FindShardByDocID("1")
		assert.ErrorIs(t, err, errors.ErrorIDNotFound)
		_, err = shard.FindDocumentByDocID("1")
		assert.ErrorIs(t, err, errors.ErrorIDNotFound)
		readers, err := index.GetReaders(0, 0)
		assert.NoError(t, err)
		assert.Empty(t, readers)
		assert.NoError(t, index.reopen())
		resp, err := MultiSearch([]string{name}, &meta.ZedQuery{Query: matchAll, Size: 10})
		assert.NoError(t, err)
		assert.Empty(t, resp.Hits.Hits)
	})

	t.Run("object store fails", func(t *testing.T) {
		store.SetErr(assert.AnError)
		defer store.SetErr(nil)
		_, err := shard.FindShardByDocID("1")
		assert.ErrorIs(t, err, errors.ErrorIDNotFound)
		_, err = shard.FindDocumentByDocID("1")
		assert.ErrorIs(t, err, errors.ErrorIDNotFound)
		_, err = index.GetReaders(0, 0)
		assert.Error(t, err)
		assert.Error(t, index.reopen())
		assert.Error(t, copyShards(StorageTypeS3, StorageTypeDisk, names))
		assert.Error(t, index.dropOldCopy(TierHot, StorageTypeS3, names))
		assert.Error(t, MoveIndexTier(name, TierHot))
		assert.True(t, index.IsReadOnly(), "a failed move keeps the index read-only")
		assert.Equal(t, TierCold, index.GetTier())
	})

	assert.NoError(t, index.dropOldCopy(TierHot, StorageTypeDisk, names))
}

func TestIndex_setLocationWithoutSettings(t *testing.T) {
	name := "TestIndex_setLocationWithoutSettings.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.Nil(t, index.ref.Settings)
	t.Cleanup(func() { assert.NoError(t, metadata.Index.Delete(name)) })
	require.NoError(t, index.setLocation(StorageTypeDisk, TierWarm))
	assert.Equal(t, TierWarm, index.GetTier())
}

func TestMoveIndexTier_Failures(t *testing.T) {
	store := useObjectStore(t)

	t.Run("shards cannot be opened", func(t *testing.T) {
		name := "TestMoveIndexTier_Failures.index_1"
		index, err := NewIndex(name, StorageTypeDisk, 1)
		require.NoError(t, err)
		require.NoError(t, StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
		index.retired.Store(true)
		err = MoveIndexTier(name, TierWarm)
		index.retired.Store(false)
		assert.Error(t, err)
		assert.False(t, index.IsReadOnly())
		assert.Equal(t, TierHot, index.GetTier())
	})

	t.Run("flush fails", func(t *testing.T) {
		index, shard := openTestShard(t, "TestMoveIndexTier_Failures.index_2")
		require.NoError(t, shard.wal.Close())
		assert.Error(t, MoveIndexTier(index.GetName(), TierWarm))
		assert.False(t, index.IsReadOnly())
		assert.Equal(t, TierHot, index.GetTier())
	})

	t.Run("reopen fails", func(t *testing.T) {
		name := "TestMoveIndexTier_Failures.index_3"
		index := newTierIndex(t, name)
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
		store.SetErr(assert.AnError)
		err := MoveIndexTier(name, TierCold)
		store.SetErr(nil)
		assert.Error(t, err)
		assert.Equal(t, TierCold, index.GetTier(), "the new location was stored")
	})
}

// skipWithoutPermissions skips tests relying on directory permissions.
func skipWithoutPermissions(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced")
	}
}

// The move is done once the new location is stored; a local copy that cannot
// be removed is reported, and left to DeleteIndex, which removes it best effort.
func TestMoveIndexTier_DropOldCopyFails(t *testing.T) {
	skipWithoutPermissions(t)
	useObjectStore(t)
	name := "TestMoveIndexTier_DropOldCopyFails.index_1"
	index := newTierIndex(t, name)
	dir := filepath.Join(config.Global.DataPath, name)
	shardDir := filepath.Join(dir, anyShard(index).GetID())
	require.NoError(t, os.Chmod(shardDir, 0o500))
	t.Cleanup(func() {
		assert.NoError(t, os.Chmod(shardDir, 0o750))
		assert.NoError(t, os.RemoveAll(dir))
	})

	assert.Error(t, MoveIndexTier(name, TierCold))
	assert.Equal(t, TierCold, index.GetTier())
	assert.NoError(t, DeleteIndex(name))
	_, err := os.Stat(shardDir)
	assert.NoError(t, err, "the local copy is still there")
}

func TestIndex_flushAndCloseWaitsForWAL(t *testing.T) {
	index, shard := openTestShard(t, "TestIndex_flushAndCloseWaitsForWAL.index_1")
	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "one"}, false))
	require.True(t, shard.walPending())

	added := make(chan struct{})
	go func() {
		defer close(added)
		time.Sleep(30 * time.Millisecond)
		ZED_INDEX_SHARD_WAL_LIST.Add(shard)
	}()
	require.NoError(t, index.flushAndClose())
	<-added
	n, err := countDocs(index)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), n)
}

func TestTierStorageTypeDefault(t *testing.T) {
	useObjectStore(t)
	old := config.Global.Tier.ColdStorageType
	config.Global.Tier.ColdStorageType = ""
	defer func() { config.Global.Tier.ColdStorageType = old }()
	got, err := tierStorageType(TierWarm)
	assert.NoError(t, err)
	assert.Equal(t, StorageTypeS3, got)
}

// A writer requested while the shard is being closed is not opened.
func TestIndexShard_GetWriterClosedMeanwhile(t *testing.T) {
	_, shard := openTestShard(t, "TestIndexShard_GetWriterClosedMeanwhile.index_1")

	shard.lock.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := shard.GetWriter()
		done <- err
	}()
	waitBlocked(t, "core.(*IndexShard).getWriter(", "sync.RWMutex.RLock", 1)
	atomic.StoreUint64(&shard.open, 0)
	err := shard.closeWALLocked()
	shard.lock.Unlock()
	require.NoError(t, err)
	assert.ErrorIs(t, <-done, ErrIndexReadOnly)
}

// Once the document is found on the latest second layer shard, the lookups
// still queued on the older ones are canceled.
func TestIndexShard_FindCancelsOtherShards(t *testing.T) {
	index, shard := openTestShard(t, "TestIndexShard_FindCancelsOtherShards.index_1")
	addSecondShard(t, shard)
	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "one"}, false))
	require.True(t, shard.ConsumeWAL())

	// one lookup at a time: the older shard is searched after the cancel
	old := config.Global.Shard.GoroutineNum
	config.Global.Shard.GoroutineNum = 1
	defer func() { config.Global.Shard.GoroutineNum = old }()
	id, err := shard.FindShardByDocID("1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)
	hit, err := shard.FindDocumentByDocID("1")
	require.NoError(t, err)
	assert.Equal(t, "1", hit.ID)
}

// The old location is closed after the new one is stored: a WAL reopened by
// a request during the copy may fail to close.
func TestMoveIndexTier_CloseOldLocationFails(t *testing.T) {
	useObjectStore(t)
	name := "TestMoveIndexTier_CloseOldLocationFails.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	shard := anyShard(index)

	// the move stops when it stores the new location
	index.lock.RLock()
	done := make(chan error, 1)
	go func() { done <- MoveIndexTier(name, TierWarm) }()
	waitBlocked(t, "core.(*Index).setLocation(", "sync.RWMutex.Lock", 1)
	err = shard.OpenWAL()
	if err == nil {
		ZED_INDEX_SHARD_WAL_LIST.Remove(shard.GetShardName())
		err = shard.wal.Close()
	}
	index.lock.RUnlock()
	require.NoError(t, err)

	assert.Error(t, <-done)
	assert.Equal(t, TierWarm, index.GetTier(), "the new location was stored")
}
