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

package cluster

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fencePartitions = 8

func newTestFence(t *testing.T, store Store, nodeID string) (*Fence, *Assigner) {
	t.Helper()
	assigner := NewAssigner(store, "/test/", fencePartitions)
	return NewFence(assigner, nodeID, fencePartitions), assigner
}

func TestFenceOwner(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	fence, assigner := newTestFence(t, store, "node-a")
	const index = "logs-2024"
	partition := PartitionOfN(index, fencePartitions)

	// nothing assigned yet: no owner, but writes are allowed locally
	owner, epoch, ok := fence.Owner(index)
	assert.False(t, ok)
	assert.Empty(t, owner)
	assert.Zero(t, epoch)
	assert.True(t, fence.IsLocalOwner(index))
	assert.NoError(t, fence.CheckEpoch(index, 0))

	assignment, ok, err := assigner.Set(ctx, partition, "node-a", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, fence.Refresh(ctx))

	owner, epoch, ok = fence.Owner(index)
	assert.True(t, ok)
	assert.Equal(t, "node-a", owner)
	assert.Equal(t, assignment.Epoch, epoch)
	assert.True(t, fence.IsLocalOwner(index))
	assert.Equal(t, map[string]int{"node-a": 1}, fence.PartitionCounts())
}

// A watch event queued before a Refresh must not roll back newer ownership.
func TestFenceIgnoresStaleEvent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	fence, assigner := newTestFence(t, store, "node-a")
	const index = "logs-2024"
	partition := PartitionOfN(index, fencePartitions)

	first, ok, err := assigner.Set(ctx, partition, "node-a", 0)
	require.NoError(t, err)
	require.True(t, ok)
	second, ok, err := assigner.Set(ctx, partition, "node-b", first.Epoch)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, fence.Refresh(ctx))

	key := assigner.AssignPrefix() + strconv.Itoa(partition)
	fence.apply(Event{Type: EventPut, KV: KV{Key: key, Value: []byte(`{"owner":"node-a"}`), Revision: first.Epoch}})
	owner, epoch, ok := fence.Owner(index)
	require.True(t, ok)
	assert.Equal(t, "node-b", owner)
	assert.Equal(t, second.Epoch, epoch)

	// a newer delete still applies
	fence.apply(Event{Type: EventDelete, KV: KV{Key: key, Revision: second.Epoch + 1}})
	_, _, ok = fence.Owner(index)
	assert.False(t, ok)
}

func TestFenceCheckEpoch(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	fence, assigner := newTestFence(t, store, "node-a")
	const index = "logs-2024"
	partition := PartitionOfN(index, fencePartitions)

	first, ok, err := assigner.Set(ctx, partition, "node-a", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, fence.Refresh(ctx))

	tests := []struct {
		name      string
		prepare   func(t *testing.T)
		epoch     func() int64
		wantErr   bool
		wantOwner string
	}{
		{
			name:    "current epoch passes",
			prepare: func(t *testing.T) {},
			epoch:   func() int64 { return first.Epoch },
		},
		{
			name:    "stale epoch is rejected",
			prepare: func(t *testing.T) {},
			epoch:   func() int64 { return first.Epoch - 1 },
			wantErr: true, wantOwner: "node-a",
		},
		{
			name: "partition moved during the request",
			prepare: func(t *testing.T) {
				_, ok, err := assigner.Set(ctx, partition, "node-b", first.Epoch)
				require.NoError(t, err)
				require.True(t, ok)
				require.NoError(t, fence.Refresh(ctx))
			},
			epoch:   func() int64 { return first.Epoch },
			wantErr: true, wantOwner: "node-b",
		},
		{
			name: "unassigned after a delete",
			prepare: func(t *testing.T) {
				require.NoError(t, store.Delete(ctx, "/test/assign/"+strconv.Itoa(partition)))
				require.NoError(t, fence.Refresh(ctx))
			},
			epoch:   func() int64 { return first.Epoch },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.prepare(t)
			err := fence.CheckEpoch(index, tt.epoch())
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			if tt.wantOwner == "" {
				assert.ErrorIs(t, err, ErrNoOwner)
				return
			}
			var epochErr *EpochError
			require.True(t, errors.As(err, &epochErr))
			assert.Equal(t, tt.wantOwner, epochErr.Owner)
			assert.Equal(t, index, epochErr.Index)
		})
	}
}

func TestFenceWatchTracksHandoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	fence, assigner := newTestFence(t, store, "node-a")
	const index = "logs-2024"
	partition := PartitionOfN(index, fencePartitions)

	go fence.Run(ctx, time.Minute)

	first, ok, err := assigner.Set(ctx, partition, "node-a", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		owner, epoch, ok := fence.Owner(index)
		return ok && owner == "node-a" && epoch == first.Epoch
	}, 2*time.Second, 5*time.Millisecond)
	assert.True(t, fence.IsLocalOwner(index))

	_, ok, err = assigner.Set(ctx, partition, "node-b", first.Epoch)
	require.NoError(t, err)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		owner, _, ok := fence.Owner(index)
		return ok && owner == "node-b"
	}, 2*time.Second, 5*time.Millisecond)

	assert.False(t, fence.IsLocalOwner(index))
	assert.Error(t, fence.CheckEpoch(index, first.Epoch))
}

// A replica the owner drops from the in-sync set learns it from the watch.
func TestFenceWatchTracksInSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	fence, assigner := newTestFence(t, store, "node-b")
	const index = "logs-2024"
	partition := PartitionOfN(index, fencePartitions)
	owned, ok, err := assigner.Set(ctx, partition, "node-a", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, assigner.UpdateInSync(ctx, index, []string{"node-b"}, owned.Epoch))

	go fence.Run(ctx, time.Minute)
	require.Eventually(t, func() bool { return fence.IsLocalInSync(index) }, 2*time.Second, 5*time.Millisecond)
	assert.False(t, fence.IsLocalInSync("other"))

	require.NoError(t, assigner.UpdateInSync(ctx, index, []string{}, owned.Epoch))
	require.Eventually(t, func() bool { return !fence.IsLocalInSync(index) }, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, assigner.UpdateInSync(ctx, index, []string{"node-b"}, owned.Epoch))
	require.Eventually(t, func() bool { return fence.IsLocalInSync(index) }, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, store.Delete(ctx, assigner.InSyncKey()))
	require.Eventually(t, func() bool { return !fence.IsLocalInSync(index) }, 2*time.Second, 5*time.Millisecond)
}

// An in-sync event queued before a Refresh must not undo the newer state the
// Refresh read, a removed replica would otherwise serve reads again.
func TestFenceIgnoresStaleInSyncEvent(t *testing.T) {
	ctx := context.Background()
	fence, assigner := newTestFence(t, NewMemoryStore(), "node-b")
	owned, ok, err := assigner.Set(ctx, PartitionOfN("logs", fencePartitions), "node-a", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"node-b"}, owned.Epoch))
	_, added, err := assigner.ListInSync(ctx)
	require.NoError(t, err)
	require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{}, owned.Epoch))

	require.NoError(t, fence.Refresh(ctx))
	assert.False(t, fence.IsLocalInSync("logs"))
	stale := Event{Type: EventPut, KV: KV{Key: assigner.InSyncKey(), Value: []byte(`{"logs":["node-b"]}`), Revision: added}}
	fence.applyInSync(stale)
	assert.False(t, fence.IsLocalInSync("logs"), "the queued add is older than the removal")

	fence.applyInSync(Event{Type: EventPut, KV: KV{Key: assigner.InSyncKey(), Value: []byte(`{"logs":["node-b"]}`), Revision: added + 100}})
	assert.True(t, fence.IsLocalInSync("logs"), "a newer event applies")
}

// A failed in-sync read still publishes the ownership read before it.
func TestFenceRefreshInSyncFails(t *testing.T) {
	store := newFaultStore(failOn(errFault, "/test/insync", "Get"))
	fence, assigner := newTestFence(t, store, "node-a")
	_, ok, err := assigner.Set(context.Background(), PartitionOfN("logs", fencePartitions), "node-b", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.ErrorIs(t, fence.Refresh(context.Background()), errFault)
	owner, _, ok := fence.Owner("logs")
	assert.True(t, ok)
	assert.Equal(t, "node-b", owner)
}

// A Refresh that finds the in-sync key gone clears the membership a missed
// delete left behind, and still ignores the event that set it. A watch event
// that lands during that read is newer than it and stays.
func TestFenceRefreshClearsAbsentInSync(t *testing.T) {
	tests := []struct {
		name   string
		during int64 // revision of an event applied while Refresh reads, 0 for none
		want   bool
	}{
		{name: "missed delete", want: false},
		{name: "event during the read", during: 6, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFaultStore(nil)
			fence, assigner := newTestFence(t, store, "node-b")
			put := func(revision int64) Event {
				return Event{Type: EventPut, KV: KV{Key: assigner.InSyncKey(), Value: []byte(`{"logs":["node-b"]}`), Revision: revision}}
			}
			fence.applyInSync(put(5))
			require.True(t, fence.IsLocalInSync("logs"))
			if tt.during > 0 {
				store.setRule(func(op, key string) error {
					if op == "Get" && key == assigner.InSyncKey() {
						fence.applyInSync(put(tt.during))
					}
					return nil
				})
			}

			require.NoError(t, fence.Refresh(context.Background()))
			assert.Equal(t, tt.want, fence.IsLocalInSync("logs"))
			fence.applyInSync(put(5))
			assert.Equal(t, tt.want, fence.IsLocalInSync("logs"), "the queued add is older than the read")
		})
	}
}

func TestFenceDefaultsAndError(t *testing.T) {
	fence := NewFence(NewAssigner(NewMemoryStore(), "/test/", 0), "node-a", 0)
	assert.Equal(t, DefaultPartitions, fence.partitions)
	assert.Equal(t, "node-a", fence.NodeID())

	err := &EpochError{Index: "logs", Owner: "node-b", Have: 3, Want: 5}
	assert.Equal(t, `cluster: partition of index "logs" moved to "node-b" (epoch 5, presented 3)`, err.Error())
}

func TestFenceRefreshReplicasFails(t *testing.T) {
	store := newFaultStore(failOn(errFault, "/test/replicas", "Get"))
	fence, _ := newTestFence(t, store, "node-a")
	assert.ErrorIs(t, fence.Refresh(context.Background()), errFault)
}

func TestFenceRunLogsFailures(t *testing.T) {
	store := newFaultStore(failOn(errFault, "/test/", "Watch", "List"))
	fence, _ := newTestFence(t, store, "node-a")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	fence.Run(ctx, 5*time.Millisecond)
	assert.Empty(t, fence.Assignments())
}

func TestFenceRunStopsOnClosedWatch(t *testing.T) {
	store := NewMemoryStore()
	fence, _ := newTestFence(t, store, "node-a")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		fence.Run(ctx, time.Minute)
	}()
	// closing the store ends every watch stream, Run keeps going until ctx ends
	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.watchers) == 3
	}, time.Second, time.Millisecond)
	require.NoError(t, store.Close())
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
}

func TestFenceApplyIgnoresBadEvents(t *testing.T) {
	fence, assigner := newTestFence(t, NewMemoryStore(), "node-a")
	prefix := assigner.AssignPrefix()

	fence.apply(Event{Type: EventPut, KV: KV{Key: prefix, Revision: 1}})
	fence.apply(Event{Type: EventPut, KV: KV{Key: prefix + "x", Revision: 2}})
	fence.apply(Event{Type: EventPut, KV: KV{Key: prefix + "1", Value: []byte("{"), Revision: 3}})
	assert.Empty(t, fence.Assignments())

	fence.applyReplicas(Event{Type: EventPut, KV: KV{Key: "/other", Value: []byte(`{"1":["b"]}`)}})
	fence.applyReplicas(Event{Type: EventPut, KV: KV{Key: assigner.ReplicasKey(), Value: []byte("{")}})
	fence.applyInSync(Event{Type: EventPut, KV: KV{Key: "/other", Value: []byte(`{"logs":["node-a"]}`)}})
	fence.applyInSync(Event{Type: EventPut, KV: KV{Key: assigner.InSyncKey(), Value: []byte("{")}})
	assert.False(t, fence.IsLocalInSync("logs"))
	fence.mu.RLock()
	defer fence.mu.RUnlock()
	assert.Empty(t, fence.replicas)
}
