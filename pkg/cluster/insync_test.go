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
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateInSync(t *testing.T) {
	ctx := context.Background()
	assigner := NewAssigner(NewMemoryStore(), "/test/insync/", 8)

	insync, rev, err := assigner.ListInSync(ctx)
	require.NoError(t, err)
	assert.Empty(t, insync)
	assert.Zero(t, rev)

	require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"node-c", "node-b"}, 0))
	require.NoError(t, assigner.UpdateInSync(ctx, "empty", []string{}, 0))
	insync, rev, err = assigner.ListInSync(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"logs": {"node-b", "node-c"}, "empty": {}}, insync)

	// unchanged sets do not write
	require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"node-b", "node-c"}, 0))
	_, again, err := assigner.ListInSync(ctx)
	require.NoError(t, err)
	assert.Equal(t, rev, again)

	require.NoError(t, assigner.UpdateInSync(ctx, "logs", nil, 0))
	require.NoError(t, assigner.UpdateInSync(ctx, "missing", nil, 0))
	insync, _, err = assigner.ListInSync(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"empty": {}}, insync)
}

// slowReadStore widens the window between the read and the swap of an
// in-sync update and counts the swaps that lost.
type slowReadStore struct {
	Store
	lost atomic.Int64
}

func (s *slowReadStore) Get(ctx context.Context, key string) (KV, error) {
	kv, err := s.Store.Get(ctx, key)
	time.Sleep(time.Millisecond)
	return kv, err
}

func (s *slowReadStore) CompareAndSwap(ctx context.Context, key string, value []byte, rev int64, lease LeaseID, guards ...Guard) (int64, bool, error) {
	newRev, swapped, err := s.Store.CompareAndSwap(ctx, key, value, rev, lease, guards...)
	if err == nil && !swapped {
		s.lost.Add(1)
	}
	return newRev, swapped, err
}

// A replica that dies fails the writes of every index it follows at once:
// the owner's updates of the shared key must not use up each other's retries.
func TestUpdateInSyncConcurrentIndexes(t *testing.T) {
	const indexes = 4 * maxInSyncRetries
	ctx := context.Background()
	store := &slowReadStore{Store: NewMemoryStore()}
	assigner := NewAssigner(store, "/test/insync-concurrent/", 8)

	errs := make([]error, indexes)
	var wg sync.WaitGroup
	for i := range indexes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = assigner.UpdateInSync(ctx, "index-"+strconv.Itoa(i), []string{"node-b"}, 0)
		}()
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
	assert.Zero(t, store.lost.Load(), "updates of one node do not race each other")
	insync, _, err := assigner.ListInSync(ctx)
	require.NoError(t, err)
	assert.Len(t, insync, indexes)
}

func TestEligibleOwners(t *testing.T) {
	const partitions = 1 // every index shares partition 0
	tests := []struct {
		name   string
		insync map[string][]string
		want   map[int][]string
	}{
		{name: "nothing replicated", insync: map[string][]string{}, want: map[int][]string{}},
		{name: "one index", insync: map[string][]string{"a": {"n2", "n1"}}, want: map[int][]string{0: {"n1", "n2"}}},
		{name: "intersection", insync: map[string][]string{"a": {"n1", "n2"}, "b": {"n2", "n3"}}, want: map[int][]string{0: {"n2"}}},
		{name: "empty entry blocks", insync: map[string][]string{"a": {"n1"}, "b": {}}, want: map[int][]string{0: {}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EligibleOwners(tt.insync, partitions))
		})
	}
}

func TestConstrainChanges(t *testing.T) {
	owners := map[int]string{0: "dead", 1: "n1", 2: "dead", 3: "dead"}
	nodes := []string{"n1", "n2", "n3"}
	eligible := map[int][]string{0: {"n3"}, 1: {"n3"}, 2: {}}
	changes := map[int]string{0: "n2", 1: "n2", 2: "n2", 3: "n2"}

	got := ConstrainChanges(changes, owners, nodes, eligible)
	assert.Equal(t, map[int]string{
		0: "n3", // dead owner: moved to the in-sync replica instead
		// 1: live owner keeps a partition its target is not in sync for
		// 2: no in-sync replica alive, the partition stays unavailable
		3: "n2", // not replicated, unconstrained
	}, got)

	assert.Equal(t, changes, ConstrainChanges(changes, owners, nodes, nil))
}

func TestRebalanceKeepsInSyncOwner(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	prefix := "/test/insync-rebalance/"
	assigner := NewAssigner(store, prefix, 1)

	gone, ok, err := assigner.Set(ctx, 0, "gone", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{}, gone.Epoch))

	membership := NewMembership(store, prefix, Node{ID: "n1"}, time.Minute, time.Second)
	require.NoError(t, membership.Join(ctx))
	election := NewElection(store, prefix, "n1", time.Minute, time.Second)
	won, err := election.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, won)
	coordinator := NewCoordinator(election, membership, assigner, 1, 0, time.Second)
	require.NoError(t, coordinator.RebalanceOnce(ctx))

	current, err := assigner.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, "gone", current[0].Owner, "a stale node must not be promoted")

	require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"n1"}, gone.Epoch))
	require.NoError(t, coordinator.RebalanceOnce(ctx))
	current, err = assigner.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, "n1", current[0].Owner)
}

// The owner drops a replica from the in-sync set and acknowledges a write
// the replica does not have, while the leader is about to promote that
// replica from the set it read before: whichever commits second must fail.
func TestInSyncAndAssignmentAreFenced(t *testing.T) {
	ctx := context.Background()

	t.Run("owner drops the replica first", func(t *testing.T) {
		assigner := NewAssigner(NewMemoryStore(), "/test/insync-fenced-a/", 1)
		owned, ok, err := assigner.Set(ctx, 0, "n1", 0)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"n2"}, owned.Epoch))

		current, err := assigner.List(ctx)
		require.NoError(t, err)
		_, read, err := assigner.ListInSync(ctx)
		require.NoError(t, err)

		require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{}, owned.Epoch))
		require.NoError(t, assigner.Apply(ctx, current, map[int]string{0: "n2"}, read))

		current, err = assigner.List(ctx)
		require.NoError(t, err)
		assert.Equal(t, "n1", current[0].Owner, "a replica dropped from the in-sync set must not be promoted")
	})

	t.Run("leader moves the partition first", func(t *testing.T) {
		assigner := NewAssigner(NewMemoryStore(), "/test/insync-fenced-b/", 1)
		owned, ok, err := assigner.Set(ctx, 0, "n1", 0)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"n2"}, owned.Epoch))

		current, err := assigner.List(ctx)
		require.NoError(t, err)
		_, read, err := assigner.ListInSync(ctx)
		require.NoError(t, err)
		require.NoError(t, assigner.Apply(ctx, current, map[int]string{0: "n2"}, read))

		err = assigner.UpdateInSync(ctx, "logs", []string{}, owned.Epoch)
		assert.ErrorContains(t, err, "partition moved", "the deposed owner must not acknowledge the write")
		var moved *EpochError
		require.ErrorAs(t, err, &moved, "callers tell a move from a store failure by type")
		assert.Equal(t, "n2", moved.Owner)
		assert.Equal(t, owned.Epoch, moved.Have)
		insync, _, err := assigner.ListInSync(ctx)
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{"logs": {"n2"}}, insync)
	})

	t.Run("an index replicated meanwhile blocks the move", func(t *testing.T) {
		assigner := NewAssigner(NewMemoryStore(), "/test/insync-fenced-c/", 1)
		owned, ok, err := assigner.Set(ctx, 0, "n1", 0)
		require.NoError(t, err)
		require.True(t, ok)
		current, err := assigner.List(ctx)
		require.NoError(t, err)

		// the in-sync key did not exist when the leader read it
		require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{}, owned.Epoch))
		require.NoError(t, assigner.Apply(ctx, current, map[int]string{0: "n2"}, 0))

		current, err = assigner.List(ctx)
		require.NoError(t, err)
		assert.Equal(t, "n1", current[0].Owner)
	})

	t.Run("assignment read fails", func(t *testing.T) {
		store := newFaultStore(nil)
		assigner := NewAssigner(store, "/test/insync-fenced-d/", 1)
		store.setRule(func(op, key string) error {
			switch {
			case op == "CompareAndSwap":
				return errLose
			case op == "Get" && key == assigner.key(0):
				return errFault
			}
			return nil
		})
		assert.ErrorIs(t, assigner.UpdateInSync(ctx, "logs", []string{}, 0), errFault)
	})

	t.Run("a deposed owner fails even when nothing changes", func(t *testing.T) {
		assigner := NewAssigner(NewMemoryStore(), "/test/insync-fenced-e/", 1)
		owned, ok, err := assigner.Set(ctx, 0, "n1", 0)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"n2"}, owned.Epoch))
		require.NoError(t, assigner.UpdateInSync(ctx, "logs", []string{"n2"}, owned.Epoch), "an unchanged set is accepted")
		require.NoError(t, assigner.UpdateInSync(ctx, "absent", nil, owned.Epoch))

		_, ok, err = assigner.Set(ctx, 0, "n2", owned.Epoch)
		require.NoError(t, err)
		require.True(t, ok)
		assert.ErrorContains(t, assigner.UpdateInSync(ctx, "logs", []string{"n2"}, owned.Epoch), "partition moved")
		assert.ErrorContains(t, assigner.UpdateInSync(ctx, "absent", nil, owned.Epoch), "partition moved")
	})
}

func TestInSyncFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("get fails", func(t *testing.T) {
		assigner := NewAssigner(newFaultStore(failOn(errFault, "/test/insync", "Get")), "/test/", 8)
		_, _, err := assigner.ListInSync(ctx)
		assert.ErrorIs(t, err, errFault)
		assert.ErrorIs(t, assigner.UpdateInSync(ctx, "idx", []string{"a"}, 0), errFault)
	})
	t.Run("bad value", func(t *testing.T) {
		store := NewMemoryStore()
		assigner := NewAssigner(store, "/test/", 8)
		_, err := store.Put(ctx, assigner.InSyncKey(), []byte("["), NoLease)
		require.NoError(t, err)
		_, _, err = assigner.ListInSync(ctx)
		assert.Error(t, err)
	})
	t.Run("swap fails", func(t *testing.T) {
		assigner := NewAssigner(newFaultStore(failOn(errFault, "/test/insync", "CompareAndSwap")), "/test/", 8)
		assert.ErrorIs(t, assigner.UpdateInSync(ctx, "idx", []string{"a"}, 0), errFault)
	})
	t.Run("swap keeps losing", func(t *testing.T) {
		assigner := NewAssigner(newFaultStore(failOn(errLose, "/test/insync", "CompareAndSwap")), "/test/", 8)
		assert.ErrorContains(t, assigner.UpdateInSync(ctx, "idx", []string{"a"}, 0), "kept changing")
	})
}
