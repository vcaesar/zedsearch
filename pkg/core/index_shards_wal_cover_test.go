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
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	blugeindex "github.com/vcaesar/riot/index"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/wal"
)

// openTestShard creates a one shard disk index with an open WAL that only the
// test consumes, the background consumer does not see it.
func openTestShard(t *testing.T, name string) (*Index, *IndexShard) {
	t.Helper()
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	shard := anyShard(index)
	require.NoError(t, shard.OpenWAL())
	ZED_INDEX_SHARD_WAL_LIST.Remove(shard.GetShardName())
	return index, shard
}

// closeWALLog closes the log below the open shard WAL so that every WAL call
// fails, the log is opened again when the test ends.
func closeWALLog(t *testing.T, shard *IndexShard) {
	t.Helper()
	require.NoError(t, shard.wal.Close())
	t.Cleanup(func() {
		l, err := wal.Open(shard.GetShardName())
		require.NoError(t, err)
		shard.lock.Lock()
		shard.wal = l
		shard.lock.Unlock()
	})
}

// addSecondShard rolls the shard over to a new second layer shard.
func addSecondShard(t *testing.T, shard *IndexShard) {
	t.Helper()
	old := config.Global.Shard.MaxSize
	config.Global.Shard.MaxSize = 0
	err := shard.CheckShards()
	config.Global.Shard.MaxSize = old
	require.NoError(t, err)
}

func walEntry(id, action string, shard int64, fields map[string]interface{}) map[string]interface{} {
	doc := map[string]interface{}{
		meta.IDFieldName:     id,
		meta.ActionFieldName: action,
		meta.ShardFieldName:  float64(shard),
	}
	for k, v := range fields {
		doc[k] = v
	}
	return doc
}

func TestIndexShardWALList_Len(t *testing.T) {
	index, err := NewIndex("TestIndexShardWALList_Len.index_1", StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(index.GetName())) })

	shard := anyShard(index)
	require.NoError(t, shard.OpenWAL())
	n := ZED_INDEX_SHARD_WAL_LIST.Len()
	assert.Positive(t, n)
	ZED_INDEX_SHARD_WAL_LIST.Remove(shard.GetShardName())
	assert.Equal(t, n-1, ZED_INDEX_SHARD_WAL_LIST.Len())
}

func TestIndexShard_OpenWALStates(t *testing.T) {
	t.Run("wal already set", func(t *testing.T) {
		_, shard := openTestShard(t, "TestIndexShard_OpenWALStates.index_1")
		atomic.StoreUint64(&shard.open, 0)
		assert.NoError(t, shard.OpenWAL())
		atomic.StoreUint64(&shard.open, 1)
	})

	t.Run("retired", func(t *testing.T) {
		index, err := NewIndex("TestIndexShard_OpenWALStates.index_2", StorageTypeDisk, 1)
		require.NoError(t, err)
		require.NoError(t, StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(index.GetName())) })
		index.retired.Store(true)
		assert.ErrorIs(t, anyShard(index).OpenWAL(), ErrIndexReadOnly)
	})

	t.Run("open fails", func(t *testing.T) {
		index, err := NewIndex("TestIndexShard_OpenWALStates.index_3", StorageTypeDisk, 1)
		require.NoError(t, err)
		require.NoError(t, StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(index.GetName())) })
		shard := anyShard(index)
		// a file where the shard directory goes
		dir := filepath.Join(config.Global.DataPath, index.GetName())
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, shard.GetID()), nil, 0o600))
		assert.Error(t, shard.OpenWAL())
		_, err = shard.GetWriter()
		assert.Error(t, err)
		_, err = index.GetDocument("1")
		assert.Error(t, err)
		assert.Error(t, index.CreateDocument("1", map[string]interface{}{"name": "one"}, false))
		assert.Error(t, index.DeleteDocument("1"))
		assert.Error(t, index.UpdateDocument("1", map[string]interface{}{"name": "one"}, true))
	})

	t.Run("rollback fails", func(t *testing.T) {
		index, err := NewIndex("TestIndexShard_OpenWALStates.index_4", StorageTypeDisk, 1)
		require.NoError(t, err)
		require.NoError(t, StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(index.GetName())) })
		shard := anyShard(index)
		l, err := wal.Open(shard.GetShardName())
		require.NoError(t, err)
		require.NoError(t, l.Redo.Write(RedoActionRead, []byte("bad")))
		require.NoError(t, l.Close())
		assert.Error(t, shard.OpenWAL())
		assert.Nil(t, shard.wal)
	})
}

func TestIndexShard_RollbackCases(t *testing.T) {
	insert := `{"@_id":"1","@_action":"insert","@_shard":0,"name":"one"}`
	tests := []struct {
		name    string
		entries []string
		read    string
		write   string
		retired bool
		wantErr bool
	}{
		{name: "invalid read redo", read: "bad", wantErr: true},
		{name: "invalid read min", read: "x:1", wantErr: true},
		{name: "invalid read max", read: "1:x", wantErr: true},
		{name: "invalid write redo", read: "1:1", write: "bad", wantErr: true},
		{name: "already committed", read: "1:1", write: "1:1"},
		{name: "missing entry", read: "1:1", wantErr: true},
		{name: "invalid entry", entries: []string{"not json"}, read: "1:1", wantErr: true},
		{name: "skipped entry", entries: []string{"{}"}, read: "1:1"},
		{name: "rolled back", entries: []string{insert}, read: "1:1"},
		{name: "write fails", entries: []string{insert}, read: "1:1", retired: true, wantErr: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index, shard := openTestShard(t, "TestIndexShard_RollbackCases.index_"+strconv.Itoa(i))
			for _, entry := range tt.entries {
				require.NoError(t, shard.writeWAL([]byte(entry)))
			}
			require.NoError(t, shard.wal.Redo.Write(RedoActionRead, []byte(tt.read)))
			if tt.write != "" {
				require.NoError(t, shard.wal.Redo.Write(RedoActionWrite, []byte(tt.write)))
			}
			index.retired.Store(tt.retired)
			err := shard.Rollback()
			index.retired.Store(false)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestIndexShard_ConsumeWALClosedLog(t *testing.T) {
	index, shard := openTestShard(t, "TestIndexShard_ConsumeWALClosedLog.index_1")
	closeWALLog(t, shard)

	assert.False(t, shard.ConsumeWAL())
	assert.False(t, shard.walPending())
	assert.Error(t, index.WaitApplied(time.Second))
	assert.Error(t, index.SyncWAL())
	assert.Zero(t, index.GetWALSize())
	assert.Error(t, ApplyReplicaEntries(index, shard.GetID(), [][]byte{[]byte(`{}`)}))

	WALHook = func(string, string, []byte) {}
	err := shard.writeWAL([]byte(`{}`))
	WALHook = nil
	assert.Error(t, err)

	var storageErr *StorageError
	assert.ErrorAs(t, index.CreateDocument("1", map[string]interface{}{"name": "one"}, false), &storageErr)
}

func TestIndexShard_ConsumeWALInvalidRedo(t *testing.T) {
	index, shard := openTestShard(t, "TestIndexShard_ConsumeWALInvalidRedo.index_1")
	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "one"}, false))
	require.NoError(t, shard.wal.Redo.Write(RedoActionWrite, []byte("bad")))

	assert.False(t, shard.ConsumeWAL())
	assert.False(t, shard.walPending())
	_, err := shard.committedIndex()
	assert.Error(t, err)
	assert.Error(t, index.WaitApplied(time.Second))
	_, err = index.SnapshotTo(t.TempDir())
	assert.Error(t, err)
	assert.Error(t, index.Flush())
}

func TestIndexShard_ConsumeWALCases(t *testing.T) {
	doc := map[string]interface{}{"name": "one"}

	t.Run("committed ahead of the log", func(t *testing.T) {
		index, shard := openTestShard(t, "TestIndexShard_ConsumeWALCases.index_1")
		require.NoError(t, index.CreateDocument("1", doc, false))
		require.NoError(t, shard.wal.Redo.Write(RedoActionWrite, []byte("0:5")))
		assert.False(t, shard.ConsumeWAL())
	})

	t.Run("invalid entry", func(t *testing.T) {
		_, shard := openTestShard(t, "TestIndexShard_ConsumeWALCases.index_2")
		require.NoError(t, shard.writeWAL([]byte("not json")))
		assert.False(t, shard.ConsumeWAL())
	})

	t.Run("full batches", func(t *testing.T) {
		index, shard := openTestShard(t, "TestIndexShard_ConsumeWALCases.index_3")
		require.NoError(t, index.CreateDocument("1", doc, false))
		require.NoError(t, index.CreateDocument("2", doc, false))
		old := config.Global.BatchSize
		config.Global.BatchSize = 1
		consumed := shard.ConsumeWAL()
		config.Global.BatchSize = old
		assert.True(t, consumed)
		n, err := countDocs(index)
		require.NoError(t, err)
		assert.Equal(t, uint64(2), n)
	})

	t.Run("full batch fails", func(t *testing.T) {
		index, shard := openTestShard(t, "TestIndexShard_ConsumeWALCases.index_4")
		require.NoError(t, index.CreateDocument("1", doc, false))
		old := config.Global.BatchSize
		config.Global.BatchSize = 1
		index.retired.Store(true)
		consumed := shard.ConsumeWAL()
		index.retired.Store(false)
		config.Global.BatchSize = old
		assert.False(t, consumed)
	})

	t.Run("last batch fails", func(t *testing.T) {
		index, shard := openTestShard(t, "TestIndexShard_ConsumeWALCases.index_5")
		require.NoError(t, index.CreateDocument("1", doc, false))
		index.retired.Store(true)
		consumed := shard.ConsumeWAL()
		index.retired.Store(false)
		assert.False(t, consumed)
	})

	t.Run("new second shard fails", func(t *testing.T) {
		index, shard := openTestShard(t, "TestIndexShard_ConsumeWALCases.index_6")
		require.NoError(t, index.CreateDocument("1", doc, false))
		// a file where the next second layer shard goes
		next := filepath.Join(config.Global.DataPath, filepath.FromSlash(shard.secondShardName(1)))
		require.NoError(t, os.WriteFile(next, nil, 0o600))
		old := config.Global.Shard.MaxSize
		config.Global.Shard.MaxSize = 0
		consumed := shard.ConsumeWAL()
		config.Global.Shard.MaxSize = old
		assert.True(t, consumed, "the entries were applied")
		assert.Equal(t, int64(2), shard.GetShardNum())
		assert.Error(t, index.CheckShards())
	})
}

// hasDoc reports whether the second layer shard id of shard holds docID.
func hasDoc(t *testing.T, shard *IndexShard, id int64, docID string) bool {
	t.Helper()
	w, err := shard.GetWriter(id)
	require.NoError(t, err)
	r, err := w.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, r.Close()) }()
	req := riot.NewTopNSearch(1, riot.NewTermQuery(docID).SetField("_id")).WithStandardAggregations()
	dmi, err := r.Search(context.Background(), req)
	require.NoError(t, err)
	return dmi.Aggregations().Count() > 0
}

// The merged actions of a document decide whether it ends up on the target
// second layer shard, and an update or delete also removes it from the older
// ones when all of them are written.
func TestWalMergeDocs_WriteToShardActions(t *testing.T) {
	_, shard := openTestShard(t, "TestWalMergeDocs_WriteToShardActions.index_1")
	addSecondShard(t, shard)
	require.Equal(t, int64(2), shard.GetShardNum())
	latest := shard.GetLatestShardID()
	fields := map[string]interface{}{"name": "one"}

	const ins, upd, del = meta.ActionTypeInsert, meta.ActionTypeUpdate, meta.ActionTypeDelete
	for _, shardID := range []int64{0, ShardIDNeedLatest, ShardIDNeedUpdate} {
		target := shardID
		if shardID < 0 {
			target = latest
		}
		for _, actions := range [][]string{
			{ins}, {upd}, {del},
			{ins, ins}, {ins, upd}, {ins, del},
			{upd, ins}, {upd, upd}, {upd, del},
			{del, ins}, {del, upd}, {del, del},
		} {
			docID := strconv.FormatInt(shardID, 10) + "_" + strings.Join(actions, "-")
			t.Run(docID, func(t *testing.T) {
				if shardID == ShardIDNeedUpdate {
					// seeded on the older shard, as written before the new one was added
					seed := walMergeDocs{0: {docID: {docID: docID, actions: []string{ins}, data: walEntry(docID, ins, 0, fields)}}}
					require.NoError(t, seed.WriteToShard(shard, 0, blugeindex.NewBatch()))
					require.True(t, hasDoc(t, shard, 0, docID))
				}

				docs := walMergeDocs{shardID: {docID: {
					docID:   docID,
					actions: actions,
					data:    walEntry(docID, actions[0], shardID, fields),
				}}}
				require.NoError(t, docs.WriteToShard(shard, shardID, blugeindex.NewBatch()))

				last := actions[len(actions)-1]
				assert.Equal(t, last != del, hasDoc(t, shard, target, docID), "on the target shard")
				if shardID == ShardIDNeedUpdate {
					assert.Equal(t, actions[0] == ins, hasDoc(t, shard, 0, docID), "on the older shard")
				} else if target != 0 {
					assert.False(t, hasDoc(t, shard, 0, docID), "only the target shard is written")
				}
			})
		}
	}
}

func TestWalMergeDocs_WriteToShardErrors(t *testing.T) {
	index, shard := openTestShard(t, "TestWalMergeDocs_WriteToShardErrors.index_1")
	addSecondShard(t, shard)

	one := func(shardID int64, action string, fields map[string]interface{}) walMergeDocs {
		return walMergeDocs{shardID: {"1": {
			docID:   "1",
			actions: []string{action},
			data:    walEntry("1", action, shardID, fields),
		}}}
	}
	name := map[string]interface{}{"name": "one"}

	t.Run("no documents for the shard", func(t *testing.T) {
		docs := one(0, meta.ActionTypeInsert, name)
		assert.NoError(t, docs.WriteToShard(shard, 1, blugeindex.NewBatch()))
		assert.NoError(t, docs.WriteToShardRollback(shard, 1, blugeindex.NewBatch()))
	})

	t.Run("invalid action", func(t *testing.T) {
		docs := one(0, "upsert", name)
		assert.Error(t, docs.WriteToShard(shard, 0, blugeindex.NewBatch()))
	})

	t.Run("invalid timestamp", func(t *testing.T) {
		for _, ts := range []interface{}{"yesterday", true} {
			fields := map[string]interface{}{meta.TimeFieldName: ts}
			docs := one(0, meta.ActionTypeInsert, fields)
			assert.Error(t, docs.WriteToShard(shard, 0, blugeindex.NewBatch()))
			docs = one(0, meta.ActionTypeInsert, map[string]interface{}{meta.TimeFieldName: ts})
			assert.Error(t, docs.WriteToShardRollback(shard, 0, blugeindex.NewBatch()))
		}
	})

	t.Run("missing second shard", func(t *testing.T) {
		docs := one(7, meta.ActionTypeInsert, name)
		assert.Error(t, docs.WriteToShard(shard, 7, blugeindex.NewBatch()))
	})

	t.Run("rollback actions", func(t *testing.T) {
		for _, action := range []string{meta.ActionTypeInsert, meta.ActionTypeUpdate, meta.ActionTypeDelete} {
			docs := one(0, action, name)
			assert.NoError(t, docs.WriteToShardRollback(shard, 0, blugeindex.NewBatch()))
		}
		docs := one(ShardIDNeedUpdate, meta.ActionTypeInsert, name)
		assert.NoError(t, docs.WriteToShardRollback(shard, ShardIDNeedUpdate, blugeindex.NewBatch()))
	})

	t.Run("read only", func(t *testing.T) {
		index.retired.Store(true)
		defer index.retired.Store(false)
		docs := one(ShardIDNeedUpdate, meta.ActionTypeUpdate, name)
		assert.Error(t, docs.WriteToShard(shard, ShardIDNeedUpdate, blugeindex.NewBatch()))
		docs = one(0, meta.ActionTypeInsert, name)
		assert.Error(t, docs.WriteToShardRollback(shard, 0, blugeindex.NewBatch()))
	})

	t.Run("closed writers", func(t *testing.T) {
		first, err := shard.GetWriter(0)
		require.NoError(t, err)
		require.NoError(t, first.Close())
		docs := one(ShardIDNeedUpdate, meta.ActionTypeUpdate, name)
		assert.Error(t, docs.WriteToShard(shard, ShardIDNeedUpdate, blugeindex.NewBatch()), "other shard batch")

		latest, err := shard.GetWriter()
		require.NoError(t, err)
		require.NoError(t, latest.Close())
		docs = one(ShardIDNeedLatest, meta.ActionTypeInsert, name)
		assert.Error(t, docs.WriteToShard(shard, ShardIDNeedLatest, blugeindex.NewBatch()), "latest shard batch")
		docs = one(1, meta.ActionTypeInsert, name)
		batch := blugeindex.NewBatch()
		assert.Error(t, docs.WriteTo(shard, batch, false))

		// Rollback fails to apply its batch
		require.NoError(t, shard.writeWAL([]byte(`{"@_id":"1","@_action":"insert","@_shard":1,"name":"one"}`)))
		last, err := shard.wal.LastIndex()
		require.NoError(t, err)
		require.NoError(t, shard.wal.Redo.Write(RedoActionRead, []byte(strconv.FormatUint(last, 10)+":"+strconv.FormatUint(last, 10))))
		assert.Error(t, shard.Rollback())
	})
}

func TestIndexShard_FindShardByDocIDMissing(t *testing.T) {
	_, shard := openTestShard(t, "TestIndexShard_FindShardByDocIDMissing.index_1")
	addSecondShard(t, shard)
	assert.Equal(t, int64(2), shard.GetShardNum())
	ids, err := shard.FindShardByDocID("missing")
	assert.Error(t, err)
	assert.Equal(t, int64(-1), ids)
}
