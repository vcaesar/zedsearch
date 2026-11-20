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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zcluster "github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestNodesSingleNode(t *testing.T) {
	require.False(t, zcluster.Enabled(), "the test binary runs without clustering")

	c, w := utils.NewContext()
	Nodes(c)
	require.Equal(t, http.StatusOK, w.Code)

	resp := new(NodesResponse)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
	assert.False(t, resp.Enabled)
	require.Len(t, resp.Nodes, 1, "a process without clustering is a cluster of one")
	assert.True(t, resp.Nodes[0].Alive)
	assert.True(t, resp.Nodes[0].IsLeader)
	assert.NotEmpty(t, resp.Nodes[0].ID)
}

func TestPartitions(t *testing.T) {
	previous := config.Global.Cluster.Partitions
	config.Global.Cluster.Partitions = 16
	t.Cleanup(func() { config.Global.Cluster.Partitions = previous })

	t.Run("single index", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestURL(c, "/api/cluster/partitions", map[string]string{"index": "logs-2024"})
		Partitions(c)
		require.Equal(t, http.StatusOK, w.Code)

		resp := new(PartitionsResponse)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		assert.False(t, resp.Enabled)
		assert.Equal(t, 16, resp.Partitions)
		require.Len(t, resp.Owners, 1)
		assert.Equal(t, "logs-2024", resp.Owners[0].Index)
		assert.Equal(t, zcluster.PartitionOf("logs-2024"), resp.Owners[0].Partition)
		assert.True(t, resp.Owners[0].Local, "without an ownership map every index is served locally")
		assert.Empty(t, resp.Owners[0].Owner)
	})

	t.Run("whole map", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestURL(c, "/api/cluster/partitions", nil)
		Partitions(c)
		require.Equal(t, http.StatusOK, w.Code)

		resp := new(PartitionsResponse)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		assert.Empty(t, resp.Owners, "nothing is assigned while clustering is off")
	})
}

const testPartitions = 4

// startTestCluster joins node-a to an in-memory cluster named after the test
// and hands the partition of each index in owners to its node. node-b is a
// member nobody can reach; a foreign leader keeps the coordinator from
// rebalancing the partitions.
func startTestCluster(t *testing.T, owners map[string]string) {
	t.Helper()
	ctx := context.Background()
	previous := config.Global.Cluster
	t.Cleanup(func() { config.Global.Cluster = previous })
	config.Global.Cluster.Name = t.Name()
	config.Global.Cluster.NodeID = "node-a"
	config.Global.Cluster.AdvertiseAddr = "http://127.0.0.1:1"
	config.Global.Cluster.Partitions = testPartitions
	config.Global.Cluster.HeartbeatTTL = time.Minute
	config.Global.Cluster.HeartbeatInterval = 100 * time.Millisecond

	store := zcluster.NewMemoryStore()
	_, won, err := store.Create(ctx, zcluster.KeyPrefix()+"leader", []byte("node-z"), zcluster.NoLease)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, zcluster.StartWith(ctx, store))
	t.Cleanup(zcluster.Stop)
	peer := zcluster.NewMembership(store, zcluster.KeyPrefix(), zcluster.Node{ID: "node-b", Addr: "http://127.0.0.1:1"}, time.Minute, time.Second)
	require.NoError(t, peer.Join(ctx))

	assigner := zcluster.NewAssigner(store, zcluster.KeyPrefix(), testPartitions)
	for index, owner := range owners {
		_, ok, err := assigner.Set(ctx, zcluster.PartitionOfN(index, testPartitions), owner, 0)
		require.NoError(t, err)
		require.True(t, ok, "%s shares its partition with another index", index)
	}
	require.Eventually(t, func() bool {
		for index, owner := range owners {
			if got, _, ok := zcluster.Owner(index); !ok || got != owner {
				return false
			}
		}
		return true
	}, 10*time.Second, 5*time.Millisecond)
}

func TestPartitionsCluster(t *testing.T) {
	local, remote := "TestPartitionsCluster.local", "TestPartitionsCluster.remote"
	startTestCluster(t, map[string]string{local: "node-a", remote: "node-b"})

	tests := []struct {
		name  string
		index string
		owner string
		local bool
	}{
		{name: "local index", index: local, owner: "node-a", local: true},
		{name: "remote index", index: remote, owner: "node-b", local: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestURL(c, "/api/cluster/partitions", map[string]string{"index": tt.index})
			Partitions(c)
			require.Equal(t, http.StatusOK, w.Code)

			resp := new(PartitionsResponse)
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
			assert.True(t, resp.Enabled)
			assert.Equal(t, testPartitions, resp.Partitions)
			require.Len(t, resp.Owners, 1)
			assert.Equal(t, tt.index, resp.Owners[0].Index)
			assert.Equal(t, zcluster.PartitionOf(tt.index), resp.Owners[0].Partition)
			assert.Equal(t, tt.owner, resp.Owners[0].Owner)
			assert.Positive(t, resp.Owners[0].Epoch)
			assert.Equal(t, tt.local, resp.Owners[0].Local)
		})
	}

	t.Run("whole map", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestURL(c, "/api/cluster/partitions", nil)
		Partitions(c)
		require.Equal(t, http.StatusOK, w.Code)

		resp := new(PartitionsResponse)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		assert.True(t, resp.Enabled)
		require.Len(t, resp.Owners, 2)
		assert.Less(t, resp.Owners[0].Partition, resp.Owners[1].Partition, "sorted by partition")
		for _, owner := range resp.Owners {
			assert.Positive(t, owner.Epoch)
			assert.Equal(t, owner.Owner == "node-a", owner.Local, owner.Owner)
		}
	})
}
