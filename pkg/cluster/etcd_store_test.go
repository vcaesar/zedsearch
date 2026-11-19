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
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	zerrors "github.com/vcaesar/zedsearch/pkg/errors"
)

// etcdTestStore dials the etcd in ZED_ETCD_ENDPOINTS, or an in-process fake
// etcd when it is unset. Every run works under its own prefix, so concurrent
// runs do not collide.
func etcdTestStore(t *testing.T) (*EtcdStore, string) {
	t.Helper()
	endpoints := os.Getenv("ZED_ETCD_ENDPOINTS")
	if endpoints == "" {
		endpoints = startFakeEtcd(t).addr
	}

	previous := config.Global.Etcd
	config.Global.Etcd.Endpoints = strings.Split(endpoints, ",")
	config.Global.Etcd.Username = os.Getenv("ZED_ETCD_USERNAME")
	config.Global.Etcd.Password = os.Getenv("ZED_ETCD_PASSWORD")

	store, err := NewEtcdStore()
	require.NoError(t, err)

	prefix := "/zed-test/" + strconv.FormatInt(time.Now().UnixNano(), 10) + "/"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		kvs, err := store.List(ctx, prefix)
		if err == nil {
			for _, kv := range kvs {
				if err := store.Delete(ctx, kv.Key); err != nil {
					t.Logf("cleanup %s: %v", kv.Key, err)
				}
			}
		}
		assert.NoError(t, store.Close())
		config.Global.Etcd = previous
	})
	return store, prefix
}

func TestEtcdStoreIntegration(t *testing.T) {
	store, prefix := etcdTestStore(t)
	ctx := context.Background()

	t.Run("membership", func(t *testing.T) {
		first := NewMembership(store, prefix, Node{ID: "node-a", Addr: "http://a:4080"}, 5*time.Second, time.Second)
		second := NewMembership(store, prefix, Node{ID: "node-b", Addr: "http://b:4080"}, 5*time.Second, time.Second)
		require.NoError(t, first.Join(ctx))
		require.NoError(t, second.Join(ctx))
		defer func() { assert.NoError(t, first.Leave(ctx)) }()

		nodes, err := first.ListNodes(ctx)
		require.NoError(t, err)
		require.Len(t, nodes, 2)

		require.NoError(t, first.KeepAlive(ctx))
		require.NoError(t, second.Leave(ctx))

		nodes, err = first.ListNodes(ctx)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "node-a", nodes[0].ID)
	})

	t.Run("election", func(t *testing.T) {
		first := NewElection(store, prefix, "node-a", 5*time.Second, time.Second)
		second := NewElection(store, prefix, "node-b", 5*time.Second, time.Second)

		won, err := first.Campaign(ctx)
		require.NoError(t, err)
		require.True(t, won)

		won, err = second.Campaign(ctx)
		require.NoError(t, err)
		assert.False(t, won)
		assert.Equal(t, "node-a", second.Leader())

		require.NoError(t, first.Resign(ctx))
		won, err = second.Campaign(ctx)
		require.NoError(t, err)
		assert.True(t, won)
		require.NoError(t, second.Resign(ctx))
	})

	t.Run("assignment epochs grow", func(t *testing.T) {
		assigner := NewAssigner(store, prefix, 8)
		first, ok, err := assigner.Set(ctx, 3, "node-a", 0)
		require.NoError(t, err)
		require.True(t, ok)

		second, ok, err := assigner.Set(ctx, 3, "node-b", first.Epoch)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Greater(t, second.Epoch, first.Epoch)

		_, ok, err = assigner.Set(ctx, 3, "node-c", first.Epoch)
		require.NoError(t, err)
		assert.False(t, ok, "a stale epoch must not win")

		assignments, err := assigner.List(ctx)
		require.NoError(t, err)
		assert.Equal(t, "node-b", assignments[3].Owner)
	})

	t.Run("fence follows the watch", func(t *testing.T) {
		watchCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		assigner := NewAssigner(store, prefix, 8)
		fence := NewFence(assigner, "node-a", 8)
		go fence.Run(watchCtx, time.Minute)

		const index = "etcd-index"
		partition := PartitionOfN(index, 8)
		current, err := assigner.List(ctx)
		require.NoError(t, err)
		assignment, ok, err := assigner.Set(ctx, partition, "node-a", current[partition].Epoch)
		require.NoError(t, err)
		require.True(t, ok)

		require.Eventually(t, func() bool {
			owner, epoch, ok := fence.Owner(index)
			return ok && owner == "node-a" && epoch == assignment.Epoch
		}, 10*time.Second, 50*time.Millisecond)
		assert.NoError(t, fence.CheckEpoch(index, assignment.Epoch))
		assert.Error(t, fence.CheckEpoch(index, assignment.Epoch-1))
	})
}

// fakeEtcdStore dials a fresh fake etcd, the fake is returned for failure
// injection.
func fakeEtcdStore(t *testing.T) (*EtcdStore, *fakeEtcd) {
	t.Helper()
	fake := startFakeEtcd(t)
	previous := config.Global.Etcd
	t.Cleanup(func() { config.Global.Etcd = previous })
	config.Global.Etcd.Endpoints = []string{fake.addr}
	config.Global.Etcd.Username = ""
	config.Global.Etcd.Password = ""

	store, err := NewEtcdStore()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, store.Close()) })
	return store, fake
}

func TestNewEtcdStoreNoEndpoints(t *testing.T) {
	previous := config.Global.Etcd
	t.Cleanup(func() { config.Global.Etcd = previous })
	config.Global.Etcd.Endpoints = nil

	_, err := NewEtcdStore()
	assert.Error(t, err)
}

func TestEtcdStoreKV(t *testing.T) {
	store, _ := fakeEtcdStore(t)
	ctx := context.Background()

	_, err := store.Get(ctx, "/kv/missing")
	assert.ErrorIs(t, err, zerrors.ErrKeyNotFound)

	rev, err := store.Put(ctx, "/kv/a", []byte("1"), NoLease)
	require.NoError(t, err)
	kv, err := store.Get(ctx, "/kv/a")
	require.NoError(t, err)
	assert.Equal(t, KV{Key: "/kv/a", Value: []byte("1"), Revision: rev}, kv)

	_, err = store.Put(ctx, "/kv/b", []byte("2"), NoLease)
	require.NoError(t, err)
	kvs, err := store.List(ctx, "/kv/")
	require.NoError(t, err)
	require.Len(t, kvs, 2)
	assert.Equal(t, "/kv/a", kvs[0].Key)
	assert.Equal(t, "/kv/b", kvs[1].Key)

	require.NoError(t, store.Delete(ctx, "/kv/b"))
	kvs, err = store.List(ctx, "/kv/")
	require.NoError(t, err)
	assert.Len(t, kvs, 1)
}

func TestEtcdStoreCreateAndSwap(t *testing.T) {
	store, _ := fakeEtcdStore(t)
	ctx := context.Background()

	created, ok, err := store.Create(ctx, "/cas/k", []byte("a"), NoLease)
	require.NoError(t, err)
	require.True(t, ok)

	rev, ok, err := store.Create(ctx, "/cas/k", []byte("b"), NoLease)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, created, rev, "a lost create reports the existing revision")

	rev, ok, err = store.CompareAndSwap(ctx, "/cas/k", []byte("c"), created-1, NoLease)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, created, rev)

	swapped, ok, err := store.CompareAndSwap(ctx, "/cas/k", []byte("c"), created, NoLease)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Greater(t, swapped, created)

	rev, ok, err = store.CompareAndSwap(ctx, "/cas/missing", []byte("x"), 7, NoLease)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, rev, "a missing key has no revision")
}

func TestEtcdStoreLeases(t *testing.T) {
	store, _ := fakeEtcdStore(t)
	ctx := context.Background()

	lease, err := store.Grant(ctx, 100*time.Millisecond) // rounded up to one second
	require.NoError(t, err)
	assert.NotEqual(t, NoLease, lease)
	require.NoError(t, store.KeepAlive(ctx, lease))
	_, err = store.Grant(ctx, 0) // never a zero second lease
	require.NoError(t, err)

	_, err = store.Put(ctx, "/lease/k", []byte("v"), lease)
	require.NoError(t, err)
	require.NoError(t, store.Revoke(ctx, lease))
	_, err = store.Get(ctx, "/lease/k")
	assert.ErrorIs(t, err, zerrors.ErrKeyNotFound, "revoking a lease drops its keys")

	assert.ErrorIs(t, store.KeepAlive(ctx, lease), zerrors.ErrKeyNotFound)
	assert.NoError(t, store.Revoke(ctx, lease), "an expired lease is already gone")
}

func TestEtcdStoreWatch(t *testing.T) {
	store, _ := fakeEtcdStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := store.Watch(ctx, "/watch/")
	require.NoError(t, err)
	// the watch is registered asynchronously, write until it is seen
	var put Event
	require.Eventually(t, func() bool {
		if _, err := store.Put(ctx, "/watch/k", []byte("v"), NoLease); err != nil {
			return false
		}
		select {
		case put = <-events:
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}, 5*time.Second, time.Millisecond)
	assert.Equal(t, EventPut, put.Type)
	assert.Equal(t, "/watch/k", put.KV.Key)
	assert.Equal(t, []byte("v"), put.KV.Value)

	require.NoError(t, store.Delete(ctx, "/watch/k"))
	for event := range events {
		if event.Type == EventDelete {
			assert.Equal(t, "/watch/k", event.KV.Key)
			break
		}
	}

	cancel()
	require.Eventually(t, func() bool {
		_, open := <-events
		return !open
	}, 5*time.Second, time.Millisecond, "the stream closes with its context")
}

func TestEtcdStoreErrors(t *testing.T) {
	store, fake := fakeEtcdStore(t)
	ctx := context.Background()
	lease, err := store.Grant(ctx, time.Minute)
	require.NoError(t, err)
	fake.fail.Store(true)

	_, err = store.Grant(ctx, time.Second)
	assert.Error(t, err)
	assert.Error(t, store.KeepAlive(ctx, lease))
	assert.Error(t, store.Revoke(ctx, lease))
	_, err = store.Put(ctx, "/err/k", nil, NoLease)
	assert.Error(t, err)
	_, err = store.Get(ctx, "/err/k")
	assert.Error(t, err)
	_, err = store.List(ctx, "/err/")
	assert.Error(t, err)
	assert.Error(t, store.Delete(ctx, "/err/k"))
	_, _, err = store.Create(ctx, "/err/k", nil, NoLease)
	assert.Error(t, err)
	_, _, err = store.CompareAndSwap(ctx, "/err/k", nil, 0, NoLease)
	assert.Error(t, err)
}

// clusterOnFakeEtcd enables clustering against a fresh fake etcd.
func clusterOnFakeEtcd(t *testing.T) *fakeEtcd {
	t.Helper()
	fake := startFakeEtcd(t)
	previous := config.Global
	t.Cleanup(func() {
		Stop()
		config.Global = previous
	})
	config.Global.Etcd.Endpoints = []string{fake.addr}
	config.Global.Etcd.Username = ""
	config.Global.Etcd.Password = ""
	config.Global.Cluster.Enable = true
	config.Global.Cluster.Name = "etcd-cluster"
	config.Global.Cluster.NodeID = "node-a"
	config.Global.Cluster.Partitions = 4
	config.Global.Cluster.HeartbeatTTL = 15 * time.Second
	config.Global.Cluster.HeartbeatInterval = 100 * time.Millisecond
	return fake
}

func TestStartDisabled(t *testing.T) {
	previous := config.Global.Cluster
	t.Cleanup(func() { config.Global.Cluster = previous })
	config.Global.Cluster.Enable = false

	require.NoError(t, Start(context.Background()))
	assert.False(t, Enabled())
}

func TestStartOnEtcd(t *testing.T) {
	clusterOnFakeEtcd(t)

	require.NoError(t, Start(context.Background()))
	require.True(t, Enabled())
	require.NoError(t, Start(context.Background()), "a second start is a no-op")
	require.Eventually(t, func() bool { return len(Assignments()) == 4 }, 5*time.Second, 10*time.Millisecond)

	Stop()
	assert.False(t, Enabled())
}

func TestStartErrors(t *testing.T) {
	t.Run("no endpoints", func(t *testing.T) {
		clusterOnFakeEtcd(t)
		config.Global.Etcd.Endpoints = nil
		assert.Error(t, Start(context.Background()))
		assert.False(t, Enabled())
	})
	t.Run("join fails", func(t *testing.T) {
		fake := clusterOnFakeEtcd(t)
		fake.fail.Store(true)
		assert.Error(t, Start(context.Background()))
		assert.False(t, Enabled())
	})
}
