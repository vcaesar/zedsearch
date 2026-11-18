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

package core

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRiotCollector(t *testing.T) {
	name := "TestRiotCollector.index_1"
	index, err := NewIndex(name, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "riot"}, false))
	waitForDocs(t, index, 1)

	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(RiotCollector))
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP zed_riot_open_writers Shard writers currently open.
# TYPE zed_riot_open_writers gauge
zed_riot_open_writers{index="TestRiotCollector.index_1"} 1
`), "zed_riot_open_writers"))

	counters := func() map[string]float64 {
		mfs, err := reg.Gather()
		require.NoError(t, err)
		values := map[string]float64{}
		for _, mf := range mfs {
			for _, m := range mf.GetMetric() {
				if m.GetLabel()[0].GetValue() != name {
					continue
				}
				if c := m.GetCounter(); c != nil {
					values[mf.GetName()] = c.GetValue()
				}
			}
		}
		return values
	}
	// riot bumps its batch counters just after the segment becomes searchable.
	assert.Eventually(t, func() bool {
		v := counters()
		return v["zed_riot_updates_total"] >= 1 && v["zed_riot_batches_total"] >= 1
	}, 10*time.Second, 20*time.Millisecond)
	assert.Contains(t, counters(), "zed_riot_file_merge_seconds_total")
}

// TestRiotCollector_ConcurrentRollover scrapes while second shards are added
// and writers are closed; run with -race.
func TestRiotCollector_ConcurrentRollover(t *testing.T) {
	name := "TestRiotCollector_ConcurrentRollover.index_1"
	index, err := NewIndex(name, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "riot"}, false))
	waitForDocs(t, index, 1)

	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(RiotCollector))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_, err := reg.Gather()
			assert.NoError(t, err)
		}
	}()
	shard := index.GetShardByDocID("1")
	for i := 0; i < 3; i++ {
		require.NoError(t, shard.NewShard())
	}
	<-done

	assert.Len(t, index.openWriterStats(), 4)
	require.NoError(t, shard.Close())
	assert.Empty(t, index.openWriterStats())
}
