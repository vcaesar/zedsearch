/* Copyright 2026 AtomAI.
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
)

// riotSample is one zed_riot_* sample of an index.
type riotSample struct {
	shard   string // empty on the per index gauges
	counter bool
	value   float64
}

// riotSamples gathers the samples of index by metric name; other indexes of
// the process are open too and are left out.
func riotSamples(reg *prometheus.Registry, index string) (map[string][]riotSample, error) {
	mfs, err := reg.Gather()
	if err != nil {
		return nil, err
	}
	samples := map[string][]riotSample{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			var s riotSample
			match := false
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "index":
					match = l.GetValue() == index
				case "shard":
					s.shard = l.GetValue()
				}
			}
			if !match {
				continue
			}
			if c := m.GetCounter(); c != nil {
				s.counter, s.value = true, c.GetValue()
			} else {
				s.value = m.GetGauge().GetValue()
			}
			samples[mf.GetName()] = append(samples[mf.GetName()], s)
		}
	}
	return samples, nil
}

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

	// riot bumps its batch counters just after the segment becomes searchable.
	var samples map[string][]riotSample
	assert.Eventually(t, func() bool {
		var err error
		samples, err = riotSamples(reg, name)
		return err == nil && len(samples["zed_riot_updates_total"]) == 1 &&
			samples["zed_riot_updates_total"][0].value >= 1 && samples["zed_riot_batches_total"][0].value >= 1
	}, 10*time.Second, 20*time.Millisecond)

	tests := []struct {
		metric  string
		counter bool
		shard   string
	}{
		{metric: "zed_riot_open_writers"},
		{metric: "zed_riot_file_segments"},
		{metric: "zed_riot_updates_total", counter: true, shard: anyShard(index).GetID() + "/000000"},
		{metric: "zed_riot_file_merge_seconds_total", counter: true, shard: anyShard(index).GetID() + "/000000"},
	}
	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			require.Len(t, samples[tt.metric], 1)
			assert.Equal(t, tt.counter, samples[tt.metric][0].counter)
			assert.Equal(t, tt.shard, samples[tt.metric][0].shard, "counters are per writer, gauges per index")
		})
	}
	assert.Equal(t, 1.0, samples["zed_riot_open_writers"][0].value)
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

	// roll over by hand only: the WAL consumer reads the shard list as well,
	// let a pass in flight finish
	shard := index.GetShardByDocID("1")
	ZED_INDEX_SHARD_WAL_LIST.Remove(shard.GetShardName())
	time.Sleep(2 * config.Global.WalSyncInterval)

	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(RiotCollector))
	var scrapes atomic.Int64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, err := reg.Gather()
			assert.NoError(t, err)
			scrapes.Add(1)
		}
	}()
	// joined before the fixture is deleted, also when a step below fails
	t.Cleanup(func() { close(stop); <-done })
	// scraping means a scrape started after before and ended during the step
	scraping := func(t *testing.T, step func()) {
		before := scrapes.Load()
		step()
		assert.Eventually(t, func() bool { return scrapes.Load() > before+1 }, 10*time.Second, time.Millisecond)
	}

	t.Run("rollover", func(t *testing.T) {
		scraping(t, func() {
			for i := 0; i < 3; i++ {
				require.NoError(t, shard.NewShard())
			}
		})
		assert.Len(t, index.openWriterStats(), 4)
		samples, err := riotSamples(reg, name)
		require.NoError(t, err)
		require.Len(t, samples["zed_riot_open_writers"], 1)
		assert.Equal(t, 4.0, samples["zed_riot_open_writers"][0].value)
		shards := map[string]bool{}
		for _, s := range samples["zed_riot_updates_total"] {
			shards[s.shard] = true
		}
		assert.Len(t, shards, 4, "one counter series per writer")
	})
	t.Run("close", func(t *testing.T) {
		// closing hands the shard back to the WAL consumer
		ZED_INDEX_SHARD_WAL_LIST.Add(shard)
		scraping(t, func() { require.NoError(t, shard.Close()) })
		assert.Empty(t, index.openWriterStats())
		samples, err := riotSamples(reg, name)
		require.NoError(t, err)
		assert.Empty(t, samples)
	})
}
