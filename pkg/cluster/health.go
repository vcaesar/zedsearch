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

	"github.com/rs/zerolog/log"
)

// Health statuses, named like Elasticsearch names them.
const (
	HealthGreen  = "green"
	HealthYellow = "yellow"
	HealthRed    = "red"
)

// IndexHealth is the state of one index in the cluster.
type IndexHealth struct {
	// Status is red when no live node owns the index, yellow when it is
	// replicated to fewer nodes than `zed_cluster_replicas`, else green.
	Status string
	// Replicas is the number of live in-sync replicas.
	Replicas int
	// Missing is how many more replicas `zed_cluster_replicas` asks for.
	Missing int
}

// WorseHealth returns the more severe of two statuses.
func WorseHealth(a, b string) string {
	switch {
	case a == HealthRed || b == HealthRed:
		return HealthRed
	case a == HealthYellow || b == HealthYellow:
		return HealthYellow
	}
	return HealthGreen
}

// Health reports the state of every index in indexes. With clustering
// disabled the single node owns everything and all of them are green. When
// the cluster state cannot be read nothing is known, and all of them are red.
func Health(ctx context.Context, indexes []string) map[string]IndexHealth {
	out := make(map[string]IndexHealth, len(indexes))
	n := get()
	if n == nil {
		for _, index := range indexes {
			out[index] = IndexHealth{Status: HealthGreen}
		}
		return out
	}

	nodes, err := n.membership.ListNodes(ctx)
	var insync map[string][]string
	if err == nil {
		insync, _, err = n.assigner.ListInSync(ctx)
	}
	if err != nil {
		log.Error().Err(err).Msg("cluster: read health")
		for _, index := range indexes {
			out[index] = IndexHealth{Status: HealthRed}
		}
		return out
	}

	alive := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		alive[node.ID] = true
	}
	want := replicaCount()
	for _, index := range indexes {
		owner, _, _ := n.fence.Owner(index)
		replicas, replicated := insync[index]
		out[index] = indexHealth(owner, alive, replicas, replicated, want)
	}
	return out
}

// indexHealth judges one index. owner is empty while its partition is
// unassigned; an index without an in-sync entry is not replicated between
// nodes and wants no replica.
func indexHealth(owner string, alive map[string]bool, insync []string, replicated bool, want int) IndexHealth {
	health := IndexHealth{Status: HealthGreen}
	if replicated {
		seen := make(map[string]bool, len(insync))
		for _, node := range insync {
			if node != owner && alive[node] && !seen[node] {
				seen[node] = true
				health.Replicas++
			}
		}
		health.Missing = max(want-health.Replicas, 0)
	}
	switch {
	case !alive[owner]:
		health.Status = HealthRed
	case health.Missing > 0:
		health.Status = HealthYellow
	}
	return health
}
