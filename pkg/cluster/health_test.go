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

	"github.com/vcaesar/zedsearch/pkg/config"
)

func TestIndexHealth(t *testing.T) {
	alive := map[string]bool{"n1": true, "n2": true}
	tests := []struct {
		name       string
		owner      string
		insync     []string
		replicated bool
		want       int
		health     IndexHealth
	}{
		{name: "not replicated", owner: "n1", want: 1, health: IndexHealth{Status: HealthGreen}},
		{name: "replicated and in sync", owner: "n1", insync: []string{"n2"}, replicated: true, want: 1,
			health: IndexHealth{Status: HealthGreen, Replicas: 1}},
		{name: "no replica caught up", owner: "n1", insync: []string{}, replicated: true, want: 1,
			health: IndexHealth{Status: HealthYellow, Missing: 1}},
		{name: "in-sync replica died", owner: "n1", insync: []string{"gone"}, replicated: true, want: 1,
			health: IndexHealth{Status: HealthYellow, Missing: 1}},
		{name: "fewer replicas than asked", owner: "n1", insync: []string{"n2"}, replicated: true, want: 2,
			health: IndexHealth{Status: HealthYellow, Replicas: 1, Missing: 1}},
		{name: "a replica listed twice counts once", owner: "n1", insync: []string{"n2", "n2"}, replicated: true, want: 2,
			health: IndexHealth{Status: HealthYellow, Replicas: 1, Missing: 1}},
		{name: "owner is not counted as its replica", owner: "n1", insync: []string{"n1"}, replicated: true, want: 1,
			health: IndexHealth{Status: HealthYellow, Missing: 1}},
		{name: "no replicas asked", owner: "n1", insync: []string{}, replicated: true, want: 0,
			health: IndexHealth{Status: HealthGreen}},
		{name: "owner died", owner: "gone", insync: []string{"n2"}, replicated: true, want: 1,
			health: IndexHealth{Status: HealthRed, Replicas: 1}},
		{name: "owner died, not replicated", owner: "gone", want: 1, health: IndexHealth{Status: HealthRed}},
		{name: "unassigned", owner: "", want: 1, health: IndexHealth{Status: HealthRed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.health, indexHealth(tt.owner, alive, tt.insync, tt.replicated, tt.want))
		})
	}
}

func TestWorseHealth(t *testing.T) {
	assert.Equal(t, HealthGreen, WorseHealth(HealthGreen, HealthGreen))
	assert.Equal(t, HealthYellow, WorseHealth(HealthGreen, HealthYellow))
	assert.Equal(t, HealthYellow, WorseHealth(HealthYellow, HealthGreen))
	assert.Equal(t, HealthRed, WorseHealth(HealthYellow, HealthRed))
	assert.Equal(t, HealthRed, WorseHealth(HealthRed, HealthGreen))
}

func TestHealth(t *testing.T) {
	ctx := context.Background()
	green := map[string]IndexHealth{"logs": {Status: HealthGreen}}
	assert.Equal(t, green, Health(ctx, []string{"logs"}), "a single node owns everything")

	startTestCluster(t, "node-a", "", 4)
	config.Global.Cluster.Replicas = 1 // restored with the cluster config
	require.Eventually(t, func() bool { return len(Assignments()) == 4 }, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, green, Health(ctx, []string{"logs"}))

	// replicated, but no replica has caught up
	_, epoch, _ := Owner("logs")
	require.NoError(t, UpdateInSync(ctx, "logs", []string{}, epoch))
	assert.Equal(t, map[string]IndexHealth{"logs": {Status: HealthYellow, Missing: 1}}, Health(ctx, []string{"logs"}))

	// the owner is gone and no in-sync replica can take over: the leader
	// leaves the partition where it is
	current := Assignments()[PartitionOfN("logs", 4)]
	_, ok, err := get().assigner.Set(ctx, current.Partition, "gone", current.Epoch)
	require.NoError(t, err)
	require.True(t, ok)
	require.Eventually(t, func() bool { return !IsLocalOwner("logs") }, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, map[string]IndexHealth{"logs": {Status: HealthRed, Missing: 1}}, Health(ctx, []string{"logs"}))
}

func TestHealthUnknownIsRed(t *testing.T) {
	ctx := context.Background()
	red := map[string]IndexHealth{"logs": {Status: HealthRed}}

	store := startFaultCluster(t, 15*time.Second, 10*time.Millisecond)
	store.setRule(failOn(errFault, KeyPrefix()+"nodes/", "List"))
	assert.Equal(t, red, Health(ctx, []string{"logs"}), "the member list cannot be read")

	store.setRule(failOn(errFault, KeyPrefix()+"insync", "Get"))
	assert.Equal(t, red, Health(ctx, []string{"logs"}), "the in-sync set cannot be read")
}
