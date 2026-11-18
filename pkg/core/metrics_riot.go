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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
or production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package core

import (
	"fmt"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	blugeindex "github.com/vcaesar/riot/index"
)

type riotMetric struct {
	desc      *prometheus.Desc
	valueType prometheus.ValueType
	value     func(s *blugeindex.Stats) float64
}

// riotCounter describes a counter of one shard writer. Writers restart from
// zero on their own, so a sum over an index would decrease, and rate() would
// take that for a reset of the whole sum: aggregate after rate() instead.
func riotCounter(name, help string, value func(s *blugeindex.Stats) float64) riotMetric {
	desc := prometheus.NewDesc("zed_riot_"+name, help, []string{"index", "shard"}, nil)
	return riotMetric{desc, prometheus.CounterValue, value}
}

// riotGauge describes a gauge summed over the open shard writers of an index.
func riotGauge(name, help string, value func(s *blugeindex.Stats) float64) riotMetric {
	desc := prometheus.NewDesc("zed_riot_"+name, help, []string{"index"}, nil)
	return riotMetric{desc, prometheus.GaugeValue, value}
}

func nanos(v uint64) float64 { return time.Duration(v).Seconds() }

// riotMetrics are the riot writer stats exported at scrape time.
var riotMetrics = []riotMetric{
	riotCounter("updates_total", "Documents inserted or updated.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotUpdates) }),
	riotCounter("deletes_total", "Document ids removed from the writer, by deletes and by updates replacing a document.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotDeletes) }),
	riotCounter("batches_total", "Batches applied.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotBatches) }),
	riotCounter("errors_total", "Writer errors: failed batches and asynchronous errors.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotOnErrors) }),
	riotCounter("indexed_bytes_total", "Plain text bytes indexed.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotIndexedPlainTextBytes) }),
	riotCounter("analysis_seconds_total", "Time spent analyzing documents.",
		func(s *blugeindex.Stats) float64 { return nanos(s.TotAnalysisTime) }),
	riotCounter("index_seconds_total", "Time spent building segments.",
		func(s *blugeindex.Stats) float64 { return nanos(s.TotIndexTime) }),
	riotCounter("batch_intro_seconds_total", "Time spent introducing batches.",
		func(s *blugeindex.Stats) float64 { return nanos(s.TotBatchIntroTime) }),
	riotCounter("persist_errors_total", "Persister loop errors.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotPersistLoopErr) }),
	riotCounter("persisted_segments_total", "Segments persisted to the directory.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotPersistedSegments) }),
	riotCounter("file_merges_total", "File merge tasks completed.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotFileMergePlanTasksDone) }),
	// a failed task also fails its loop pass, so the loop counter alone counts it once
	riotCounter("file_merge_errors_total", "File merge loop errors, failed merge tasks included.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotFileMergeLoopErr) }),
	riotCounter("file_merge_seconds_total", "Time spent merging file segments.",
		func(s *blugeindex.Stats) float64 { return nanos(s.TotFileMergeZapTime) }),
	riotGauge("file_segments", "File segments in the current root snapshot.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotFileSegmentsAtRoot) }),
	riotGauge("memory_segments", "In-memory segments in the current root snapshot.",
		func(s *blugeindex.Stats) float64 { return float64(s.TotMemorySegmentsAtRoot) }),
	riotGauge("open_writers", "Shard writers currently open.",
		func(*blugeindex.Stats) float64 { return 1 }),
}

// RiotCollector exports the stats of every open riot writer at scrape time.
var RiotCollector prometheus.Collector = riotCollector{}

type riotCollector struct{}

func (riotCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range riotMetrics {
		ch <- m.desc
	}
}

func (riotCollector) Collect(ch chan<- prometheus.Metric) {
	for _, index := range ZED_INDEX_LIST.List() {
		writers := index.openWriterStats()
		if len(writers) == 0 {
			continue
		}
		name := index.GetName()
		for _, m := range riotMetrics {
			if m.valueType == prometheus.CounterValue {
				for i := range writers {
					ch <- prometheus.MustNewConstMetric(m.desc, m.valueType, m.value(&writers[i].stats), name, writers[i].shard)
				}
				continue
			}
			var sum float64
			for i := range writers {
				sum += m.value(&writers[i].stats)
			}
			ch <- prometheus.MustNewConstMetric(m.desc, m.valueType, sum, name)
		}
	}
}

// writerStats are the stats of the open writer of a second layer shard,
// named as in its data path: <shard>/<second shard>.
type writerStats struct {
	shard string
	stats blugeindex.Stats
}

// openWriterStats returns the stats of the shard writers already open, without opening any.
// Second shards are appended under index.lock, writers are opened under the second shard lock
// and closed under the shard lock, so each snapshot is taken under the lock its mutation uses.
func (index *Index) openWriterStats() []writerStats {
	index.lock.RLock()
	shards := make(map[*IndexShard][]*IndexSecondShard, len(index.shards))
	for _, shard := range index.shards {
		shards[shard] = slices.Clone(shard.shards)
	}
	index.lock.RUnlock()

	var stats []writerStats
	for shard, seconds := range shards {
		shard.lock.RLock()
		for _, second := range seconds {
			second.lock.RLock()
			if second.writer != nil {
				// riot has no cheaper snapshot: Status also asks the directory for its
				// size, a listing on disk and the cached one on object storage.
				stats = append(stats, writerStats{
					shard: fmt.Sprintf("%s/%06x", shard.GetID(), second.ref.ID),
					stats: second.writer.Status(),
				})
			}
			second.lock.RUnlock()
		}
		shard.lock.RUnlock()
	}
	return stats
}
