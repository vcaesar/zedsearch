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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vcaesar/riot"
	blugeindex "github.com/vcaesar/riot/index"
)

type riotMetric struct {
	desc      *prometheus.Desc
	valueType prometheus.ValueType
	value     func(s *blugeindex.Stats) float64
}

func riotDesc(name, help string) *prometheus.Desc {
	return prometheus.NewDesc("zed_riot_"+name, help, []string{"index"}, nil)
}

func nanos(v uint64) float64 { return time.Duration(v).Seconds() }

// riotMetrics are the riot writer stats exported per index, summed over its open shards.
// Counters restart when a shard writer reopens, which rate() treats as a reset.
var riotMetrics = []riotMetric{
	{riotDesc("updates_total", "Documents inserted or updated."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotUpdates) }},
	{riotDesc("deletes_total", "Documents deleted."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotDeletes) }},
	{riotDesc("batches_total", "Batches applied."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotBatches) }},
	{riotDesc("errors_total", "Asynchronous writer errors."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotOnErrors) }},
	{riotDesc("indexed_bytes_total", "Plain text bytes indexed."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotIndexedPlainTextBytes) }},
	{riotDesc("analysis_seconds_total", "Time spent analyzing documents."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return nanos(s.TotAnalysisTime) }},
	{riotDesc("index_seconds_total", "Time spent building segments."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return nanos(s.TotIndexTime) }},
	{riotDesc("batch_intro_seconds_total", "Time spent introducing batches."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return nanos(s.TotBatchIntroTime) }},
	{riotDesc("persist_errors_total", "Persister loop errors."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotPersistLoopErr) }},
	{riotDesc("persisted_segments_total", "Segments persisted to the directory."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotPersistedSegments) }},
	{riotDesc("file_merges_total", "File merge tasks completed."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotFileMergePlanTasksDone) }},
	{riotDesc("file_merge_errors_total", "File merge loop and task errors."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotFileMergeLoopErr + s.TotFileMergePlanTasksErr) }},
	{riotDesc("file_merge_seconds_total", "Time spent merging file segments."), prometheus.CounterValue,
		func(s *blugeindex.Stats) float64 { return nanos(s.TotFileMergeZapTime) }},
	{riotDesc("file_segments", "File segments in the current root snapshot."), prometheus.GaugeValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotFileSegmentsAtRoot) }},
	{riotDesc("memory_segments", "In-memory segments in the current root snapshot."), prometheus.GaugeValue,
		func(s *blugeindex.Stats) float64 { return float64(s.TotMemorySegmentsAtRoot) }},
}

var riotOpenWriters = riotDesc("open_writers", "Shard writers currently open.")

// RiotCollector exports the stats of every open riot writer at scrape time.
var RiotCollector prometheus.Collector = riotCollector{}

type riotCollector struct{}

func (riotCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range riotMetrics {
		ch <- m.desc
	}
	ch <- riotOpenWriters
}

func (riotCollector) Collect(ch chan<- prometheus.Metric) {
	for _, index := range ZED_INDEX_LIST.List() {
		writers := index.openWriters()
		if len(writers) == 0 {
			continue
		}
		sums := make([]float64, len(riotMetrics))
		for _, w := range writers {
			// Status never touches the directory, unlike DirectoryStats which lists object storage.
			stats := w.Status()
			for i, m := range riotMetrics {
				sums[i] += m.value(&stats)
			}
		}
		name := index.GetName()
		for i, m := range riotMetrics {
			ch <- prometheus.MustNewConstMetric(m.desc, m.valueType, sums[i], name)
		}
		ch <- prometheus.MustNewConstMetric(riotOpenWriters, prometheus.GaugeValue, float64(len(writers)), name)
	}
}

// openWriters returns the shard writers already open, without opening any.
func (index *Index) openWriters() []*riot.Writer {
	index.lock.RLock()
	shards := make([]*IndexShard, 0, len(index.shards))
	for _, shard := range index.shards {
		shards = append(shards, shard)
	}
	index.lock.RUnlock()

	var writers []*riot.Writer
	for _, shard := range shards {
		shard.lock.RLock()
		seconds := shard.shards
		shard.lock.RUnlock()
		for _, second := range seconds {
			second.lock.RLock()
			if second.writer != nil {
				writers = append(writers, second.writer)
			}
			second.lock.RUnlock()
		}
	}
	return writers
}
