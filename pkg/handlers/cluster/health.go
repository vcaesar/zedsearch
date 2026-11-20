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

	"github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta/elastic"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// Health returns the health of the cluster
//
// A single node is always green. In a cluster an index is red while no
// live node owns it and yellow while it is replicated to fewer nodes than
// `zed_cluster_replicas`; the cluster reports its worst index.
//
// @Id ClusterHealth
// @Summary Get cluster health for compatible ES
// @security BasicAuth
// @Tags    Cluster
// @Produce json
// @Param   index  path  string  false  "Index"
// @Success 200 {object} elastic.ClusterHealthResponse
// @Router /es/_cluster/health [get]
func Health(c zhttp.Context) {
	shards := countShards(c.Context(), c.Param("target"))
	nodes := len(cluster.Nodes())
	zutils.RenderJSON(c, http.StatusOK, elastic.ClusterHealthResponse{
		ClusterName:                 clusterName(),
		Status:                      shards.status,
		TimedOut:                    false,
		NumberOfNodes:               nodes,
		NumberOfDataNodes:           nodes,
		ActivePrimaryShards:         shards.primaries,
		ActiveShards:                shards.active(),
		RelocatingShards:            0,
		InitializingShards:          0,
		UnassignedShards:            int(shards.unassigned),
		DelayedUnassignedShards:     0,
		NumberOfPendingTasks:        0,
		NumberOfInFlightFetch:       0,
		TaskMaxWaitingInQueueMillis: 0,
		ActiveShardsPercentAsNumber: shards.activePercent(),
	})
}

// shardCounts is the health of a set of indexes, counted in shards like
// Elasticsearch counts it.
type shardCounts struct {
	status     string // the worst index
	primaries  int64  // shards of indexes a live node owns
	replicas   int64  // copies on live in-sync replicas
	unassigned int64  // shards without an owner, and replica copies still missing
}

func (s shardCounts) active() int64 {
	return s.primaries + s.replicas
}

func (s shardCounts) activePercent() float64 {
	total := s.active() + s.unassigned
	if total == 0 {
		return 100.0
	}
	return float64(s.active()) * 100 / float64(total)
}

// countShards judges every index matching the target patterns.
func countShards(ctx context.Context, target string) shardCounts {
	patterns := elastic.SplitPatterns(target)
	shards := make(map[string]int64)
	names := make([]string, 0, core.ZED_INDEX_LIST.Len())
	for _, index := range core.ZED_INDEX_LIST.List() {
		if name := index.GetName(); elastic.MatchAnyPattern(name, patterns) {
			shards[name] = index.GetShardNum()
			names = append(names, name)
		}
	}

	counts := shardCounts{status: cluster.HealthGreen}
	for name, health := range cluster.Health(ctx, names) {
		counts.status = cluster.WorseHealth(counts.status, health.Status)
		if health.Status == cluster.HealthRed {
			counts.unassigned += shards[name]
		} else {
			counts.primaries += shards[name]
		}
		counts.replicas += int64(health.Replicas) * shards[name]
		counts.unassigned += int64(health.Missing) * shards[name]
	}
	return counts
}
