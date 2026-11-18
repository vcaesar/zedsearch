/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI.
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
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/vcaesar/zedsearch/pkg/config"
)

// Record opened WAL used to do consume
var ZED_INDEX_SHARD_WAL_LIST IndexShardWALList

type IndexShardWALList struct {
	Shards map[string]*IndexShard
	lock   sync.RWMutex
	cycle  sync.Mutex // a removal waits for workers using an earlier snapshot
}

func init() {
	ZED_INDEX_SHARD_WAL_LIST.Shards = make(map[string]*IndexShard)
	go ZED_INDEX_SHARD_WAL_LIST.ConsumeWAL()
}

func (t *IndexShardWALList) Add(shard *IndexShard) {
	t.lock.Lock()
	t.Shards[shard.GetShardName()] = shard
	t.lock.Unlock()
}

// Remove waits for any consumer pass using the shard, including its metadata update.
func (t *IndexShardWALList) Remove(name string) {
	t.remove(name, nil)
}

func (t *IndexShardWALList) remove(name string, expected *IndexShard) {
	t.cycle.Lock()
	defer t.cycle.Unlock()
	t.lock.Lock()
	if expected == nil || t.Shards[name] == expected {
		delete(t.Shards, name)
	}
	t.lock.Unlock()
}

func (t *IndexShardWALList) List() []*IndexShard {
	t.lock.RLock()
	shards := make([]*IndexShard, 0, len(t.Shards))
	for _, shard := range t.Shards {
		shards = append(shards, shard)
	}
	t.lock.RUnlock()
	return shards
}

func (t *IndexShardWALList) Len() int {
	t.lock.RLock()
	n := len(t.Shards)
	t.lock.RUnlock()
	return n
}

func (t *IndexShardWALList) ConsumeWAL() {
	indexes := make(map[string]*Index)
	eg := &errgroup.Group{}
	eg.SetLimit(config.Global.Shard.GoroutineNum)
	tick := time.NewTicker(config.Global.WalSyncInterval)
	for range tick.C {
		t.cycle.Lock()
		// Size the result channel from the same snapshot that starts the workers.
		shards := t.List()
		indexUpdated := make(chan string, len(shards))
		for _, shard := range shards {
			shard := shard
			indexes[shard.GetIndexName()] = shard.root
			eg.Go(func() error {
				updated := shard.ConsumeWAL()
				if updated {
					indexUpdated <- shard.GetIndexName()
				}
				return nil
			})
		}
		_ = eg.Wait()
		close(indexUpdated)

		// update index stats
		updated := false
		for name := range indexUpdated {
			updated = true
			index, ok := indexes[name]
			if !ok {
				continue
			}

			_ = index.UpdateMetadata()
			size := index.GetWALSize()
			if size == uint64(index.GetShardNum()) {
				size = 0
			}
			index.UpdateWALSize(size)

			stats := index.GetStats()
			SetMetricStatsByIndex(name, "doc_num", float64(atomic.LoadUint64(&stats.DocNum)))
			SetMetricStatsByIndex(name, "storage_size", float64(atomic.LoadUint64(&stats.StorageSize)/1024/1024)) // convert to MB

			delete(indexes, name)
		}

		t.cycle.Unlock()

		// release the batch memory, but not on idle ticks: a forced GC every
		// WalSyncInterval stalls searches for nothing
		if updated {
			runtime.GC()
		}
	}
}
