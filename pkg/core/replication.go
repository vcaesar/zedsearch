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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	blugeindex "github.com/vcaesar/riot/index"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// Hooks the cluster replication layer installs. Both are nil in a single
// node build. They are set once at startup, before any request is served.
var (
	// WALHook sees every entry a local write appends to a shard WAL, in WAL
	// order per shard. Entries applied through ApplyReplicaEntries skip it.
	WALHook func(index, shard string, entry []byte)
	// StoreGuard returning false keeps storeIndex from writing the shared
	// index metadata: a replica must not overwrite the owner's record.
	StoreGuard func(index string) bool
	// WriteGuard returning an error rejects a local write before it reaches
	// the WAL: a copy of an index another node owns must not diverge.
	WriteGuard func(index string) error
)

// IsDisk reports whether the index data lives on local disk.
func (index *Index) IsDisk() bool {
	storageType := index.GetStorageType()
	return storageType == "" || storageType == StorageTypeDisk
}

// LoadIndex returns the index, loading it from the metadata store when this
// node has not seen it yet (it was created on another node).
func LoadIndex(name string) (*Index, error) {
	if index, ok := GetIndex(name); ok {
		return index, nil
	}
	stored, err := metadata.Index.Get(name)
	if err != nil {
		return nil, err
	}
	index, err := newIndexFromMeta(stored)
	if err != nil {
		return nil, err
	}
	ZED_INDEX_LIST.lock.Lock()
	defer ZED_INDEX_LIST.lock.Unlock()
	if existing, ok := ZED_INDEX_LIST.Indexes[name]; ok {
		return existing, nil
	}
	ZED_INDEX_LIST.Indexes[name] = index
	return index, nil
}

// ApplyReplicaEntries appends WAL entries recorded on the owner to the WAL
// of the same shard here. Every entry is rewritten to an update across all
// second layer shards, which makes it idempotent: the replica's second
// layer layout may differ from the owner's, and an entry may be applied
// twice when a snapshot already contains it.
func ApplyReplicaEntries(index *Index, shardID string, entries [][]byte) error {
	if err := index.checkWritable(); err != nil {
		return err
	}
	shard, ok := index.shards[shardID]
	if !ok {
		return fmt.Errorf("index [%s] has no shard [%s]", index.GetName(), shardID)
	}
	if err := shard.OpenWAL(); err != nil {
		return err
	}
	index.writes.RLock()
	defer index.writes.RUnlock()
	shard.lock.RLock()
	defer shard.lock.RUnlock()
	if shard.wal == nil {
		return ErrIndexReadOnly
	}
	for _, entry := range entries {
		data, err := idempotentEntry(entry)
		if err != nil {
			return err
		}
		if err := shard.wal.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func idempotentEntry(entry []byte) ([]byte, error) {
	doc := make(map[string]interface{})
	if err := json.Unmarshal(entry, &doc); err != nil {
		return nil, err
	}
	if doc[meta.ActionFieldName] == meta.ActionTypeInsert {
		doc[meta.ActionFieldName] = meta.ActionTypeUpdate
	}
	doc[meta.ShardFieldName] = ShardIDNeedUpdate
	return json.Marshal(doc)
}

// WaitApplied waits until every WAL entry written before the call has been
// applied to the writers.
func (index *Index) WaitApplied(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	// the targets of all shards are taken first: entries written while one
	// shard catches up are not waited for
	targets := make(map[*IndexShard]uint64, len(index.shards))
	for _, shard := range index.shards {
		shard.lock.RLock()
		w := shard.wal
		var last uint64
		var err error
		if w != nil {
			last, err = w.LastIndex()
		}
		shard.lock.RUnlock()
		if err != nil {
			return err
		}
		if w != nil {
			targets[shard] = last
		}
	}
	for shard, last := range targets {
		for {
			committed, err := shard.committedIndex()
			if err != nil {
				return err
			}
			if committed >= last {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("shard [%s]: WAL not applied within %s", shard.GetShardName(), timeout)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

// committedIndex returns the last WAL entry applied to the writers. It fails
// once the WAL is closed, by a flush, tier move or delete: what was left to
// apply is unknown then. A WAL opened again continues the same redo log.
func (s *IndexShard) committedIndex() (uint64, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if s.wal == nil {
		return 0, fmt.Errorf("shard [%s]: WAL closed while waiting for it to be applied", s.GetShardName())
	}
	_, committed, err := s.readRedoLog(RedoActionWrite)
	if err != nil && err.Error() != errors.ErrNotFound.Error() {
		return 0, err
	}
	return committed, nil
}

// Refresh waits until the documents written so far are searchable. The WAL
// consumer applies them, so writers and WAL stay open.
func (index *Index) Refresh() error {
	return index.WaitApplied(walFlushTimeout)
}

// SyncWAL flushes the open WAL of every shard to stable storage.
func (index *Index) SyncWAL() error {
	for _, shard := range index.shards {
		shard.lock.RLock()
		var err error
		if shard.wal != nil {
			err = shard.wal.Sync()
		}
		shard.lock.RUnlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// SnapshotTo copies a point in time view of every second layer shard of a
// disk index into dir, laid out like the data path, after the WAL written
// so far was applied. It returns the index metadata of that view.
func (index *Index) SnapshotTo(dir string) ([]byte, error) {
	if !index.IsDisk() {
		return nil, fmt.Errorf("index [%s] is not on disk", index.GetName())
	}
	if err := index.WaitApplied(walFlushTimeout); err != nil {
		return nil, err
	}
	data, err := index.MarshalJSON()
	if err != nil {
		return nil, err
	}
	for _, shard := range index.shards {
		for j := int64(0); j < shard.GetShardNum(); j++ {
			r, err := shard.openReader(j)
			if err != nil {
				return nil, err
			}
			second := fmt.Sprintf("%06x", j)
			target := filepath.Join(dir, shard.GetID(), second)
			if err = os.MkdirAll(target, 0o750); err == nil {
				err = r.Backup(target, nil)
			}
			if err == nil {
				err = linkGraphs(filepath.Join(config.Global.DataPath, index.GetName(), shard.GetID(), second), target)
			}
			if closeErr := r.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return data, nil
}

// linkGraphs hard links the persisted HNSW graphs of src into dst, which
// older riot backups leave out; newer ones already put them in dst. Opening
// dst loads them instead of rebuilding them on the first knn search, and
// removes those whose segment is not there.
func linkGraphs(src, dst string) error {
	graphs, err := filepath.Glob(filepath.Join(src, "*"+blugeindex.ItemKindGraph))
	if err != nil {
		return err
	}
	for _, path := range graphs {
		err := os.Link(path, filepath.Join(dst, filepath.Base(path)))
		// a merge removed it, or the backup already holds it
		if err != nil && !os.IsNotExist(err) && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

// InstallSnapshot replaces the local copy of an index with a snapshot made
// by SnapshotTo: the open index is closed, its directory (WAL included) is
// replaced by dir and the index is rebuilt from the snapshot metadata. dir
// must be on the same file system as the data path.
func InstallSnapshot(name string, indexMeta []byte, dir string) error {
	stored := new(meta.Index)
	if err := json.Unmarshal(indexMeta, stored); err != nil {
		return err
	}
	if stored.Name != name {
		return fmt.Errorf("snapshot of [%s] cannot be installed as [%s]", stored.Name, name)
	}
	index, err := newIndexFromMeta(stored)
	if err != nil {
		return err
	}

	if old, ok := GetIndex(name); ok {
		// retire the old copy: nothing may reopen its writers or WAL on
		// files that are about to be replaced
		old.writes.Lock()
		defer old.writes.Unlock()
		old.retired.Store(true)
		if err := old.Close(); err != nil {
			return err
		}
	}
	target := filepath.Join(config.Global.DataPath, name)
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Rename(dir, target); err != nil {
		return err
	}
	ZED_INDEX_LIST.Add(index)
	return nil
}

// DropLocalIndex closes an index and removes its local files, leaving the
// shared metadata and any bucket untouched: the copy a replica keeps of an
// index its owner deleted.
func DropLocalIndex(name string) error {
	index, ok := GetIndex(name)
	if !ok {
		return nil
	}
	index.writes.Lock()
	index.retired.Store(true)
	ZED_INDEX_LIST.Delete(name)
	index.writes.Unlock()
	return os.RemoveAll(filepath.Join(config.Global.DataPath, name))
}

// Flush blocks writes, applies the WAL and closes the writers, which
// persists every segment. Writers and WAL reopen on the next request.
func (index *Index) Flush() error {
	index.writes.Lock()
	defer index.writes.Unlock()
	if err := index.WaitApplied(walFlushTimeout); err != nil {
		return err
	}
	return index.Close()
}
