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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zerrors "github.com/vcaesar/zedsearch/pkg/errors"
)

func TestElectionSingleWinner(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	first := NewElection(store, "/test/", "node-a", time.Minute, time.Second)
	second := NewElection(store, "/test/", "node-b", time.Minute, time.Second)

	won, err := first.Campaign(ctx)
	require.NoError(t, err)
	assert.True(t, won)
	assert.True(t, first.IsLeader())

	won, err = second.Campaign(ctx)
	require.NoError(t, err)
	assert.False(t, won)
	assert.False(t, second.IsLeader())
	assert.Equal(t, "node-a", second.Leader())

	// the leader keeps the key on the next round
	won, err = first.Campaign(ctx)
	require.NoError(t, err)
	assert.True(t, won)
}

func TestElectionHandoff(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	store := NewMemoryStore()
	store.SetClock(clock.Now)
	defer func() { require.NoError(t, store.Close()) }()

	ttl := 15 * time.Second
	first := NewElection(store, "/test/", "node-a", ttl, ttl/3)
	second := NewElection(store, "/test/", "node-b", ttl, ttl/3)

	tests := []struct {
		name       string
		run        func(t *testing.T)
		wantLeader string
	}{
		{
			name: "first node wins",
			run: func(t *testing.T) {
				won, err := first.Campaign(ctx)
				require.NoError(t, err)
				require.True(t, won)
			},
			wantLeader: "node-a",
		},
		{
			name: "second node waits while the leader is alive",
			run: func(t *testing.T) {
				won, err := second.Campaign(ctx)
				require.NoError(t, err)
				require.False(t, won)
			},
			wantLeader: "node-a",
		},
		{
			name: "resign hands over immediately",
			run: func(t *testing.T) {
				require.NoError(t, first.Resign(ctx))
				won, err := second.Campaign(ctx)
				require.NoError(t, err)
				require.True(t, won)
			},
			wantLeader: "node-b",
		},
		{
			name: "expired lease elects the other node",
			run: func(t *testing.T) {
				clock.Advance(2 * ttl) // node-b stopped heartbeating
				won, err := first.Campaign(ctx)
				require.NoError(t, err)
				require.True(t, won)
			},
			wantLeader: "node-a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
			kv, err := store.Get(ctx, first.Key())
			require.NoError(t, err)
			assert.Equal(t, tt.wantLeader, string(kv.Value))
		})
	}

	// the node that lost its lease must notice on its next round
	won, err := second.Campaign(ctx)
	require.NoError(t, err)
	assert.False(t, won)
	assert.False(t, second.IsLeader())
}

func TestCoordinatorRebalanceOnce(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	membership := testMembership(store, "node-a", time.Minute)
	require.NoError(t, membership.Join(ctx))
	require.NoError(t, testMembership(store, "node-b", time.Minute).Join(ctx))

	assigner := NewAssigner(store, "/test/", 8)
	election := NewElection(store, "/test/", "node-a", time.Minute, time.Second)
	coordinator := NewCoordinator(election, membership, assigner, 8, 0, time.Second)

	// a follower must not write anything
	require.NoError(t, coordinator.RebalanceOnce(ctx))
	assignments, err := assigner.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, assignments)

	won, err := election.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, coordinator.RebalanceOnce(ctx))

	assignments, err = assigner.List(ctx)
	require.NoError(t, err)
	require.Len(t, assignments, 8)
	counts := map[string]int{}
	for _, assignment := range assignments {
		counts[assignment.Owner]++
		assert.Positive(t, assignment.Epoch)
	}
	assert.Equal(t, map[string]int{"node-a": 4, "node-b": 4}, counts)

	// a second round with the same members changes nothing
	before := assignments
	require.NoError(t, coordinator.RebalanceOnce(ctx))
	after, err := assigner.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// A leader that lost its lease must not delete the key of the leader elected
// after it, else a third node takes the key while the second still leads.
func TestElectionStaleResignKeepsNewLeader(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	store := NewMemoryStore()
	store.SetClock(clock.Now)
	defer func() { require.NoError(t, store.Close()) }()

	ttl := 15 * time.Second
	a := NewElection(store, "/test/", "node-a", ttl, ttl/3)
	b := NewElection(store, "/test/", "node-b", ttl, ttl/3)
	c := NewElection(store, "/test/", "node-c", ttl, ttl/3)

	won, err := a.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, won)
	clock.Advance(2 * ttl)
	won, err = b.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, won)

	require.NoError(t, a.Resign(ctx))
	kv, err := store.Get(ctx, a.Key())
	require.NoError(t, err)
	assert.Equal(t, "node-b", string(kv.Value))

	won, err = c.Campaign(ctx)
	require.NoError(t, err)
	assert.False(t, won)
}

// A leader whose key vanished behind its back must step down, so only one
// node leads after the next rounds.
func TestElectionStepsDownWhenKeyLost(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer func() { require.NoError(t, store.Close()) }()

	b := NewElection(store, "/test/", "node-b", time.Minute, time.Second)
	c := NewElection(store, "/test/", "node-c", time.Minute, time.Second)
	won, err := b.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, won)

	require.NoError(t, store.Delete(ctx, b.Key()))
	wonC, err := c.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, wonC)
	wonB, err := b.Campaign(ctx)
	require.NoError(t, err)
	assert.False(t, wonB)
	assert.False(t, b.IsLeader())
	assert.Equal(t, "node-c", b.Leader())
}

// failingKeepAlive is a store whose lease renewals fail with a transient error.
type failingKeepAlive struct {
	Store
	fail bool
}

func (s *failingKeepAlive) KeepAlive(ctx context.Context, id LeaseID) error {
	if s.fail {
		return context.DeadlineExceeded
	}
	return s.Store.KeepAlive(ctx, id)
}

// A leader whose renewals keep failing must stop acting as leader once its
// lease may have expired, another node may own the key by then.
func TestElectionLeadershipExpiresWithoutRenewal(t *testing.T) {
	ctx := context.Background()
	store := &failingKeepAlive{Store: NewMemoryStore()}
	defer func() { require.NoError(t, store.Close()) }()

	ttl := 50 * time.Millisecond
	election := NewElection(store, "/test/", "node-a", ttl, time.Second)
	won, err := election.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, won)
	assert.True(t, election.IsLeader())

	store.fail = true
	_, err = election.Campaign(ctx)
	require.Error(t, err)
	time.Sleep(ttl)
	assert.False(t, election.IsLeader())

	// a successful renewal restores it
	store.fail = false
	won, err = election.Campaign(ctx)
	require.NoError(t, err)
	assert.True(t, won)
	assert.True(t, election.IsLeader())
}

func TestElectionCampaignFailures(t *testing.T) {
	ctx := context.Background()
	leaderKey := "/e/leader"

	t.Run("grant fails", func(t *testing.T) {
		e := NewElection(newFaultStore(failOn(errFault, "", "Grant")), "/e/", "a", time.Minute, time.Second)
		_, err := e.Campaign(ctx)
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("create and revoke fail", func(t *testing.T) {
		store := newFaultStore(func(op, key string) error {
			if op == "Create" || op == "Revoke" {
				return errFault
			}
			return nil
		})
		_, err := NewElection(store, "/e/", "a", time.Minute, time.Second).Campaign(ctx)
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("leader vanishes between create and get", func(t *testing.T) {
		store := newFaultStore(nil)
		won, err := NewElection(store, "/e/", "a", time.Minute, time.Second).Campaign(ctx)
		require.NoError(t, err)
		require.True(t, won)

		store.setRule(failOn(zerrors.ErrKeyNotFound, leaderKey, "Get"))
		second := NewElection(store, "/e/", "b", time.Minute, time.Second)
		won, err = second.Campaign(ctx)
		require.NoError(t, err)
		assert.False(t, won)
		assert.Empty(t, second.Leader())
	})
	t.Run("get of the other leader fails", func(t *testing.T) {
		store := newFaultStore(nil)
		_, err := NewElection(store, "/e/", "a", time.Minute, time.Second).Campaign(ctx)
		require.NoError(t, err)

		store.setRule(failOn(errFault, leaderKey, "Get"))
		_, err = NewElection(store, "/e/", "b", time.Minute, time.Second).Campaign(ctx)
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("renewal get fails", func(t *testing.T) {
		store := newFaultStore(nil)
		e := NewElection(store, "/e/", "a", time.Minute, time.Second)
		won, err := e.Campaign(ctx)
		require.NoError(t, err)
		require.True(t, won)

		store.setRule(failOn(errFault, leaderKey, "Get"))
		_, err = e.Campaign(ctx)
		assert.ErrorIs(t, err, errFault)
	})
}

func TestElectionResignWhenNotLeading(t *testing.T) {
	e := NewElection(NewMemoryStore(), "/e/", "a", time.Minute, time.Second)
	assert.NoError(t, e.Resign(context.Background()))
	assert.Equal(t, "/e/leader", e.Key())
}

func TestElectionRunLogsFailures(t *testing.T) {
	e := NewElection(newFaultStore(failOn(errFault, "", "Grant")), "/e/", "a", time.Minute, 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	e.Run(ctx)
	assert.False(t, e.IsLeader())
}

func TestNewCoordinatorDefaults(t *testing.T) {
	c := NewCoordinator(nil, nil, nil, 0, -1, time.Second)
	assert.Equal(t, DefaultPartitions, c.partitions)
	assert.Zero(t, c.replicas)
}

// leaderCoordinator elects "a" on store and returns its coordinator over
// prefix "/c/", with members "a" and "b" registered when join is set.
func leaderCoordinator(t *testing.T, store Store, join bool) *Coordinator {
	t.Helper()
	ctx := context.Background()
	election := NewElection(store, "/c/", "a", time.Minute, time.Second)
	won, err := election.Campaign(ctx)
	require.NoError(t, err)
	require.True(t, won)
	membership := NewMembership(store, "/c/", Node{ID: "a"}, time.Minute, time.Second)
	if join {
		require.NoError(t, membership.Join(ctx))
		require.NoError(t, NewMembership(store, "/c/", Node{ID: "b"}, time.Minute, time.Second).Join(ctx))
	}
	return NewCoordinator(election, membership, NewAssigner(store, "/c/", 4), 4, 1, time.Second)
}

func TestCoordinatorRebalanceFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("no members", func(t *testing.T) {
		store := NewMemoryStore()
		c := leaderCoordinator(t, store, false)
		require.NoError(t, c.RebalanceOnce(ctx))
		assignments, err := c.assigner.List(ctx)
		require.NoError(t, err)
		assert.Empty(t, assignments)
	})

	tests := []struct {
		name string
		rule faultRule
	}{
		{"list nodes", failOn(errFault, "/c/nodes/", "List")},
		{"list assignments", failOn(errFault, "/c/assign/", "List")},
		{"list replicas", failOn(errFault, "/c/replicas", "Get")},
		{"list in-sync", failOn(errFault, "/c/insync", "Get")},
		{"apply", failOn(errFault, "/c/assign/", "CompareAndSwap")},
		{"set replicas", failOn(errFault, "/c/replicas", "CompareAndSwap")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFaultStore(nil)
			c := leaderCoordinator(t, store, true)
			store.setRule(tt.rule)
			assert.ErrorIs(t, c.RebalanceOnce(ctx), errFault)
		})
	}

	t.Run("replica map changed concurrently", func(t *testing.T) {
		store := newFaultStore(nil)
		c := leaderCoordinator(t, store, true)
		store.setRule(failOn(errLose, "/c/replicas", "CompareAndSwap"))
		require.NoError(t, c.RebalanceOnce(ctx))
		replicas, rev, err := c.assigner.ListReplicas(ctx)
		require.NoError(t, err)
		assert.Empty(t, replicas)
		assert.Zero(t, rev)
	})
}

func TestCoordinatorRunLogsFailures(t *testing.T) {
	store := newFaultStore(nil)
	c := leaderCoordinator(t, store, true)
	c.interval = 5 * time.Millisecond
	store.setRule(failOn(errFault, "/c/", "Watch", "List"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	c.Run(ctx)
}
