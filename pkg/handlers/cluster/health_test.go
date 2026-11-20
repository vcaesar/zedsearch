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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zcluster "github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta/elastic"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestHealth(t *testing.T) {
	indexName := "TestHealth.index_1"

	t.Run("prepare", func(t *testing.T) {
		index, err := core.NewIndex(indexName, "disk", 3)
		assert.NoError(t, err)
		assert.NoError(t, core.StoreIndex(index))
	})

	type args struct {
		params     map[string]string
		wantShards int64
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "single index",
			args: args{params: map[string]string{"target": indexName}, wantShards: 3},
		},
		{
			name: "index glob",
			args: args{params: map[string]string{"target": "TestHealth.*"}, wantShards: 3},
		},
		{
			name: "unknown index",
			args: args{params: map[string]string{"target": "NotExist" + indexName}, wantShards: 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestParams(c, tt.args.params)
			Health(c)
			assert.Equal(t, http.StatusOK, w.Code)

			resp := new(elastic.ClusterHealthResponse)
			assert.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
			assert.Equal(t, ClusterName, resp.ClusterName)
			assert.Equal(t, "green", resp.Status)
			assert.False(t, resp.TimedOut)
			assert.Equal(t, 1, resp.NumberOfNodes)
			assert.Equal(t, 1, resp.NumberOfDataNodes)
			assert.Equal(t, tt.args.wantShards, resp.ActivePrimaryShards)
			assert.Equal(t, tt.args.wantShards, resp.ActiveShards)
			assert.Equal(t, 0, resp.UnassignedShards)
			assert.Equal(t, 100.0, resp.ActiveShardsPercentAsNumber)
		})
	}

	t.Run("all indexes", func(t *testing.T) {
		c, w := utils.NewContext()
		Health(c)
		assert.Equal(t, http.StatusOK, w.Code)

		resp := new(elastic.ClusterHealthResponse)
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		assert.GreaterOrEqual(t, resp.ActiveShards, int64(3))
	})

	t.Run("cleanup", func(t *testing.T) {
		assert.NoError(t, core.DeleteIndex(indexName))
	})
}

func TestHealthCluster(t *testing.T) {
	owned, replicated, orphan := "TestHealthCluster.owned", "TestHealthCluster.replicated", "TestHealthCluster.orphan"
	for _, name := range []string{owned, replicated, orphan} {
		index, err := core.NewIndex(name, "disk", 2)
		require.NoError(t, err)
		require.NoError(t, core.StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })
	}
	startTestCluster(t, map[string]string{owned: "node-a", replicated: "node-a", orphan: "node-gone"})
	config.Global.Cluster.Replicas = 1 // restored with the cluster config
	// replicated, but its replica has not caught up yet
	require.NoError(t, zcluster.UpdateInSync(context.Background(), replicated, []string{}))

	tests := []struct {
		name           string
		target         string
		status         string
		primaries      int64
		unassigned     int
		activePercent  float64
		catIndexHealth string
	}{
		{name: "owned by a live node", target: owned, status: "green", primaries: 2, activePercent: 100, catIndexHealth: "green"},
		{name: "replica missing", target: replicated, status: "yellow", primaries: 2, unassigned: 2, activePercent: 50, catIndexHealth: "yellow"},
		{name: "owner is gone", target: orphan, status: "red", unassigned: 2, activePercent: 0, catIndexHealth: "red"},
		{name: "the worst index wins", target: "TestHealthCluster.*", status: "red", primaries: 4, unassigned: 4, activePercent: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestParams(c, map[string]string{"target": tt.target})
			Health(c)
			require.Equal(t, http.StatusOK, w.Code)

			resp := new(elastic.ClusterHealthResponse)
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
			assert.Equal(t, tt.status, resp.Status)
			assert.Equal(t, tt.primaries, resp.ActivePrimaryShards)
			assert.Equal(t, tt.primaries, resp.ActiveShards)
			assert.Equal(t, tt.unassigned, resp.UnassignedShards)
			assert.Equal(t, tt.activePercent, resp.ActiveShardsPercentAsNumber)

			if tt.catIndexHealth == "" {
				return
			}
			c, w = utils.NewContext()
			utils.SetRequestParams(c, map[string]string{"target": tt.target})
			utils.SetRequestURL(c, "/es/_cat/indices", map[string]string{"format": "json"})
			CatIndices(c)
			require.Equal(t, http.StatusOK, w.Code)
			items := make([]elastic.CatIndexResponse, 0)
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
			require.Len(t, items, 1)
			assert.Equal(t, tt.catIndexHealth, items[0].Health)
		})
	}

	t.Run("cat health", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestURL(c, "/es/_cat/health", map[string]string{"format": "json"})
		CatHealth(c)
		require.Equal(t, http.StatusOK, w.Code)
		items := make([]elastic.CatHealthResponse, 0)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
		require.Len(t, items, 1)
		assert.Equal(t, "red", items[0].Status)
		assert.NotEqual(t, "0", items[0].Unassign)
		assert.NotEqual(t, "100.0%", items[0].ActiveShardsPercent)
	})
}

func TestShardCounts(t *testing.T) {
	assert.Equal(t, 100.0, shardCounts{}.activePercent(), "no shards at all is fully active")
	counts := shardCounts{primaries: 3, replicas: 3, unassigned: 2}
	assert.Equal(t, int64(6), counts.active())
	assert.Equal(t, 75.0, counts.activePercent())
}
