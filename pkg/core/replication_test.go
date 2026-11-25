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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/wal"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func waitDocs(t *testing.T, index *Index, want uint64) {
	t.Helper()
	require.Eventually(t, func() bool {
		n, err := countDocs(index)
		return err == nil && n == want
	}, 30*time.Second, 50*time.Millisecond)
}

func TestIdempotentEntry(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		wantAction string
	}{
		{name: "insert becomes update", action: meta.ActionTypeInsert, wantAction: meta.ActionTypeUpdate},
		{name: "update stays", action: meta.ActionTypeUpdate, wantAction: meta.ActionTypeUpdate},
		{name: "delete stays", action: meta.ActionTypeDelete, wantAction: meta.ActionTypeDelete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := json.Marshal(map[string]interface{}{
				meta.IDFieldName: "1", meta.ActionFieldName: tt.action, meta.ShardFieldName: 3,
			})
			require.NoError(t, err)
			out, err := idempotentEntry(entry)
			require.NoError(t, err)
			doc := map[string]interface{}{}
			require.NoError(t, json.Unmarshal(out, &doc))
			assert.Equal(t, tt.wantAction, doc[meta.ActionFieldName])
			assert.InDelta(t, float64(ShardIDNeedUpdate), doc[meta.ShardFieldName], 0)
		})
	}

	t.Run("idempotent entry passes through", func(t *testing.T) {
		for _, action := range []string{meta.ActionTypeUpdate, meta.ActionTypeDelete} {
			entry := []byte(`{"@_action":"` + action + `","@_id":"1","@_shard":-2,"emb":[0.25,1e-7]}`)
			out, err := idempotentEntry(entry)
			require.NoError(t, err)
			assert.Same(t, &entry[0], &out[0], action)
		}
	})
	t.Run("field values keep their encoding", func(t *testing.T) {
		out, err := idempotentEntry([]byte(`{"@_action":"insert","@_id":"1","@_shard":-1,"@timestamp":1795200476377767001,"emb":[0.25,1e-7]}`))
		require.NoError(t, err)
		doc := map[string]json.RawMessage{}
		require.NoError(t, json.Unmarshal(out, &doc))
		assert.Equal(t, `"update"`, string(doc[meta.ActionFieldName]))
		assert.Equal(t, "-2", string(doc[meta.ShardFieldName]))
		assert.Equal(t, "1795200476377767001", string(doc[meta.TimeFieldName]))
		assert.Equal(t, "[0.25,1e-7]", string(doc["emb"]))
	})
	t.Run("document fields spelled like the entry's own do not pass for them", func(t *testing.T) {
		for _, entry := range []string{
			`{"@_action":"insert","@_id":"1","@_shard":-1,"@_ACTION":"update","@_SHARD":-2}`,
			`{"@_action":"update","@_id":"1","@_shard":3,"@_Shard":-2}`,
			`{"@_ACTION":"delete","@_SHARD":-2,"@_action":"update","@_id":"1","@_shard":0}`,
			`{"@_action":"noop","@_id":"1","@_shard":-2}`,
		} {
			out, err := idempotentEntry([]byte(entry))
			require.NoError(t, err)
			doc := map[string]json.RawMessage{}
			require.NoError(t, json.Unmarshal(out, &doc))
			assert.Equal(t, "-2", string(doc[meta.ShardFieldName]), entry)
			assert.NotEqual(t, `"insert"`, string(doc[meta.ActionFieldName]), entry)
		}
		// rewritten, not passed through: the shard of the entry was not -2
		entry := []byte(`{"@_action":"update","@_id":"1","@_shard":3,"@_Shard":-2}`)
		out, err := idempotentEntry(entry)
		require.NoError(t, err)
		assert.NotSame(t, &entry[0], &out[0])
	})
	t.Run("escaped action is decoded", func(t *testing.T) {
		tests := []struct {
			name, entry, wantAction string
			passes                  bool
		}{
			{name: "escaped insert", entry: `{"@_action":"\u0069nsert","@_id":"1","@_shard":3}`, wantAction: `"update"`},
			{name: "escaped update", entry: `{"@_action":"upd\u0061te","@_id":"1","@_shard":-2}`, wantAction: `"upd\u0061te"`, passes: true},
			{name: "non-string action", entry: `{"@_action":1,"@_id":"1","@_shard":3}`, wantAction: `1`},
			{name: "object action", entry: `{"@_action":{"a":"insert"},"@_shard":3}`, wantAction: `{"a":"insert"}`},
			{name: "missing action", entry: `{"@_id":"1","@_shard":3}`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				entry := []byte(tt.entry)
				out, err := idempotentEntry(entry)
				require.NoError(t, err)
				if tt.passes {
					assert.Same(t, &entry[0], &out[0])
				}
				doc := map[string]json.RawMessage{}
				require.NoError(t, json.Unmarshal(out, &doc))
				assert.Equal(t, tt.wantAction, string(doc[meta.ActionFieldName]))
				assert.Equal(t, "-2", string(doc[meta.ShardFieldName]))
			})
		}
	})
	t.Run("invalid", func(t *testing.T) {
		for _, entry := range []string{
			"{",
			`{"@_action":"update","@_shard":-2,"name":tru}`,
			`{"@_action":"update","@_shard":-2,"emb":[1,}`,
			`{"@_action":"insert","@_shard":-1,"name":"one"`,
		} {
			_, err := idempotentEntry([]byte(entry))
			assert.Error(t, err, entry)
		}
	})
	t.Run("rewrite encodes as json.Marshal", func(t *testing.T) {
		for _, entry := range []string{
			`{"@_action":"insert","@_id":"1","@_shard":-1,"name":"one","price":150.5}`,
			`{"@_action":"insert","@_shard":0,"a\"b":1,"c\\d":2,"<x>&":3,"ü":4,"tab\t":5,"":6}`,
			`{"@_shard":3}`,
		} {
			out, err := idempotentEntry([]byte(entry))
			require.NoError(t, err)
			doc := map[string]json.RawMessage{}
			require.NoError(t, json.Unmarshal([]byte(entry), &doc))
			if entryAction(doc[meta.ActionFieldName]) == meta.ActionTypeInsert {
				doc[meta.ActionFieldName] = json.RawMessage(updateAction)
			}
			doc[meta.ShardFieldName] = json.RawMessage("-2")
			want, err := json.Marshal(doc)
			require.NoError(t, err)
			assert.Equal(t, string(want), string(out), entry)
		}
	})
}

func BenchmarkIdempotentEntry(b *testing.B) {
	body := strings.Repeat("w1 w100 w3 w4000 w55 ", 20)
	for name, entry := range map[string]string{
		"insert":      `{"@_action":"insert","@_id":"1","@_shard":2,"@timestamp":1795200476377767001,"body":"` + body + `","price":150.5}`,
		"passthrough": `{"@_action":"update","@_id":"1","@_shard":-2,"@timestamp":1795200476377767001,"body":"` + body + `","price":150.5}`,
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := idempotentEntry([]byte(entry)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// WriteGuard rejects a write before the WAL; replica entries bypass it.
// DropLocalIndex removes the local copy but keeps the shared metadata.
func TestWriteGuardAndDropLocalIndex(t *testing.T) {
	name := "TestWriteGuardAndDropLocalIndex.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	ZED_INDEX_LIST.Add(index)
	t.Cleanup(func() {
		ZED_INDEX_LIST.Delete(name)
		assert.NoError(t, metadata.Index.Delete(name))
	})

	WriteGuard = func(string) error { return assert.AnError }
	t.Cleanup(func() { WriteGuard = nil })
	err = index.CreateDocument("1", map[string]interface{}{"name": "one"}, false)
	assert.ErrorIs(t, err, assert.AnError)
	var storageErr *StorageError
	assert.ErrorAs(t, err, &storageErr, "a fenced write is retryable")
	shardID := ""
	for id := range index.shards {
		shardID = id
	}
	entry := []byte(`{"@_id":"1","@_action":"insert","@_shard":-1,"name":"one"}`)
	require.NoError(t, ApplyReplicaEntries(index, shardID, [][]byte{entry}))
	waitDocs(t, index, 1)
	WriteGuard = nil

	require.NoError(t, DropLocalIndex(name))
	_, ok := GetIndex(name)
	assert.False(t, ok)
	_, err = os.Stat(filepath.Join(config.Global.DataPath, name))
	assert.True(t, os.IsNotExist(err))
	require.NoError(t, DropLocalIndex(name), "dropping a missing index is a no-op")
	_, err = metadata.Index.Get(name)
	assert.NoError(t, err, "the shared metadata stays")
	assert.Error(t, index.CreateDocument("2", map[string]interface{}{}, false), "a dropped copy stays closed")
}

// The hook records owner writes; entries replayed twice on a replica stay
// one document.
func TestWALHookAndApplyReplicaEntries(t *testing.T) {
	var mu sync.Mutex
	recorded := map[string][][]byte{}
	WALHook = func(index, shard string, entry []byte) {
		mu.Lock()
		recorded[shard] = append(recorded[shard], append([]byte(nil), entry...))
		mu.Unlock()
	}
	t.Cleanup(func() { WALHook = nil })

	owner, err := NewIndex("TestWALHookAndApplyReplicaEntries.owner", StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(owner))
	require.NoError(t, owner.CreateDocument("1", map[string]interface{}{"name": "first"}, false))
	require.NoError(t, owner.CreateDocument("2", map[string]interface{}{"name": "second"}, false))
	waitDocs(t, owner, 2)
	require.NoError(t, owner.DeleteDocument("2"))
	WALHook = nil

	mu.Lock()
	require.Len(t, recorded, 1)
	mu.Unlock()

	replica, err := NewIndex("TestWALHookAndApplyReplicaEntries.replica", StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(replica))
	for shardID := range replica.shards {
		for _, entries := range recorded {
			require.NoError(t, ApplyReplicaEntries(replica, shardID, entries))
			require.NoError(t, ApplyReplicaEntries(replica, shardID, entries[:1]))
		}
	}
	waitDocs(t, replica, 1)

	assert.Error(t, ApplyReplicaEntries(replica, "missing", nil))
}

func TestSnapshotInstall(t *testing.T) {
	source := newTierIndex(t, "TestSnapshotInstall.index_1")
	require.NoError(t, source.CreateDocument("2", map[string]interface{}{"name": "second"}, false))
	require.NoError(t, source.SyncWAL())

	// stage under the data path like the replica does: t.TempDir may be on
	// another drive (Windows CI), where rename fails
	staged := filepath.Join(config.Global.DataPath, "_replication_test", "TestSnapshotInstall")
	require.NoError(t, os.RemoveAll(staged))
	require.NoError(t, os.MkdirAll(filepath.Dir(staged), 0o750))
	t.Cleanup(func() { assert.NoError(t, os.RemoveAll(filepath.Dir(staged))) })

	indexMeta, err := source.SnapshotTo(staged)
	require.NoError(t, err)

	// install over a renamed copy of the metadata, as a replica would
	stored := new(meta.Index)
	require.NoError(t, json.Unmarshal(indexMeta, stored))
	stored.Name = "TestSnapshotInstall.index_2"
	renamed, err := json.Marshal(stored)
	require.NoError(t, err)

	old, err := NewIndex("TestSnapshotInstall.index_2", StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(old))
	require.NoError(t, InstallSnapshot("TestSnapshotInstall.index_2", renamed, staged))

	installed, ok := GetIndex("TestSnapshotInstall.index_2")
	require.True(t, ok)
	assert.NotSame(t, old, installed)
	assert.ErrorIs(t, old.CreateDocument("x", map[string]interface{}{}, false), ErrIndexReadOnly, "the replaced copy is retired")
	waitDocs(t, installed, 2)

	assert.Error(t, InstallSnapshot("other", renamed, staged), "name mismatch")
	assert.Error(t, InstallSnapshot("other", []byte("{"), staged))
}

func TestSnapshotToRejectsObjectStorage(t *testing.T) {
	useObjectStore(t)
	index, err := NewIndex("TestSnapshotToRejectsObjectStorage.index_1", StorageTypeS3, 1)
	require.NoError(t, err)
	_, err = index.SnapshotTo(t.TempDir())
	assert.Error(t, err)
	assert.False(t, index.IsDisk())
}

func TestFlushAndLoadIndex(t *testing.T) {
	index := newTierIndex(t, "TestFlushAndLoadIndex.index_1")
	require.NoError(t, index.Flush())
	// writers and WAL reopen on the next write
	require.NoError(t, index.CreateDocument("2", map[string]interface{}{"name": "after flush"}, false))
	waitDocs(t, index, 2)

	loaded, err := LoadIndex("TestFlushAndLoadIndex.index_1")
	require.NoError(t, err)
	assert.Same(t, index, loaded)

	// an index only the metadata store knows is loaded from it
	ZED_INDEX_LIST.Delete("TestFlushAndLoadIndex.index_1")
	loaded, err = LoadIndex("TestFlushAndLoadIndex.index_1")
	require.NoError(t, err)
	assert.Equal(t, "TestFlushAndLoadIndex.index_1", loaded.GetName())

	_, err = LoadIndex("TestFlushAndLoadIndex.missing")
	assert.Error(t, err)
}

// Refresh returns once the documents written before it are searchable,
// and leaves the writers and the WAL open.
func TestIndexRefresh(t *testing.T) {
	name := "TestIndexRefresh.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 2)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { _ = DeleteIndex(name) })

	t.Run("nothing written", func(t *testing.T) {
		// no WAL is open, nothing to wait for
		require.NoError(t, index.Refresh())
	})
	t.Run("pending writes", func(t *testing.T) {
		const docs = 300
		for i := range docs {
			require.NoError(t, index.CreateDocument(strconv.Itoa(i), map[string]interface{}{"name": "refreshed"}, false))
		}
		require.NoError(t, index.Refresh())
		n, err := countDocs(index)
		require.NoError(t, err)
		assert.Equal(t, uint64(docs), n, "documents are searchable when Refresh returns")
		for _, shard := range index.shards {
			assert.Equal(t, uint64(1), atomic.LoadUint64(&shard.open), "Refresh closed shard %s", shard.GetID())
		}
	})
	t.Run("entries left in a closed WAL", func(t *testing.T) {
		// a restart leaves the WAL closed until the next write
		require.NoError(t, index.Close())
		shard := anyShard(index)
		require.Equal(t, uint64(0), atomic.LoadUint64(&shard.open))
		entry, err := idempotentEntry([]byte(`{"@_id":"restart","@_action":"insert","name":"pending"}`))
		require.NoError(t, err)
		w, err := wal.Open(shard.GetShardName())
		require.NoError(t, err)
		require.NoError(t, w.Write(entry))
		require.NoError(t, w.Close())

		before, err := countDocs(index)
		require.NoError(t, err)
		require.NoError(t, index.Refresh())
		n, err := countDocs(index)
		require.NoError(t, err)
		assert.Equal(t, before+1, n, "Refresh applies entries persisted before the restart")
	})
}

// Rollback opens a writer to undo an interrupted batch. A failure rebuilding
// that batch must leave both the WAL and the writer available for a retry.
func TestIndexRefreshRetriesWALRecovery(t *testing.T) {
	name := t.Name() + ".index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	shard := anyShard(index)
	mappings := meta.NewMappings()
	day := meta.NewProperty("date")
	mappings.SetProperty("day", day)
	require.NoError(t, index.SetMappings(mappings))
	w, err := wal.Open(shard.GetShardName())
	require.NoError(t, err)
	require.NoError(t, w.Write([]byte(`{"@_id":"pending","@_action":"insert","@_shard":0,"day":"2026-01-02"}`)))
	require.NoError(t, w.Redo.Write(RedoActionRead, []byte("1:1")))
	require.NoError(t, w.Close())

	for range 2 {
		require.ErrorContains(t, index.Refresh(), "field [day]")
		require.Zero(t, atomic.LoadUint64(&shard.open))
		require.Nil(t, shard.wal)
		require.Empty(t, index.openWriterStats())
		require.False(t, slices.Contains(ZED_INDEX_SHARD_WAL_LIST.List(), shard))
	}
	require.NoError(t, shard.Close())

	day.Format = time.DateOnly
	mappings.SetProperty("day", day)
	require.NoError(t, index.SetMappings(mappings))
	require.NoError(t, index.Refresh())
	require.True(t, slices.Contains(ZED_INDEX_SHARD_WAL_LIST.List(), shard))
	count, err := countDocs(index)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count)
}

// A wait neither succeeds nor panics once the WAL it waits on is closed.
func TestIndexWaitApplied(t *testing.T) {
	name := "TestIndexWaitApplied.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { _ = DeleteIndex(name) })

	shard := anyShard(index)
	require.NoError(t, shard.OpenWAL())
	// nothing applies the WAL once the consumer is gone
	ZED_INDEX_SHARD_WAL_LIST.Remove(shard.GetShardName())
	assert.True(t, index.Applied(), "an empty WAL has nothing to apply")
	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "pending"}, false))

	t.Run("pending", func(t *testing.T) {
		last, err := shard.wal.LastIndex()
		require.NoError(t, err)
		committed, err := shard.committedIndex()
		require.NoError(t, err)
		assert.Less(t, committed, last)
		assert.False(t, index.Applied())
		assert.ErrorContains(t, index.WaitApplied(50*time.Millisecond), "not applied within")
	})
	t.Run("closed", func(t *testing.T) {
		require.NoError(t, index.Close())
		_, err := shard.committedIndex()
		assert.ErrorContains(t, err, "WAL closed")
		assert.True(t, index.Applied(), "no open WAL, nothing to apply")
		assert.NoError(t, index.WaitApplied(time.Second), "no open WAL, nothing to wait for")
	})
}

func TestStoreGuard(t *testing.T) {
	StoreGuard = func(string) bool { return false }
	t.Cleanup(func() { StoreGuard = nil })
	index, err := NewIndex("TestStoreGuard.index_1", StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	StoreGuard = nil
	ZED_INDEX_LIST.Delete("TestStoreGuard.index_1")
	_, err = LoadIndex("TestStoreGuard.index_1")
	assert.Error(t, err, "a guarded index must not reach the metadata store")
}
