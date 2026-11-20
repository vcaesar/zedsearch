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

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// helperEnv makes the test binary run main() with its value as arguments.
const helperEnv = "ZED_TEST_MOCKDATA_ARGS"

func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(helperEnv); ok {
		os.Args = append([]string{"mockdata"}, strings.Fields(args)...)
		main()
		return
	}
	os.Exit(m.Run())
}

func TestMainCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		wantErr bool
		want    string
	}{
		{"seeds every dataset", "-prefix m_ -reset -products 2 -logs 2 -stores 2 -forum 5 -events 3 -end 2026-10-30T19:39:00Z", false, `mock data written with prefix \"m_\"`},
		{"invalid end", "-end 2026-10-30", true, "-end: parsing time"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(),
				helperEnv+"="+tt.args,
				"ZED_DATA_PATH="+t.TempDir(),
				"ZED_FIRST_ADMIN_USER=admin",
				"ZED_FIRST_ADMIN_PASSWORD=Complexpass#123",
				"ZED_TELEMETRY=false",
			)
			out, err := cmd.CombinedOutput()
			if tt.wantErr {
				assert.Error(t, err, string(out))
			} else {
				assert.NoError(t, err, string(out))
			}
			assert.Contains(t, string(out), tt.want)
		})
	}
}

func TestGenerators(t *testing.T) {
	end := time.Date(2026, 10, 28, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name   string
		gen    func(string, int, time.Time) []map[string]any
		step   time.Duration
		fields []string
	}{
		{"products", products, time.Hour, []string{"name", "category", "tags", "price", "stock", "in_stock", "rating", "embedding", "@timestamp"}},
		{"logs", logs, time.Minute, []string{"level", "service", "host", "message", "status", "duration_ms", "trace_id", "@timestamp"}},
		{"stores", stores, time.Hour, []string{"name", "city", "location", "open", "rating", "@timestamp"}},
		{"forum", forum, 10 * time.Minute, []string{"title", "topic", "author", "votes", "qa", "@timestamp"}},
		{"events", events, time.Second, []string{"action", "user_id", "country", "device", "category", "amount", "latency_ms", "@timestamp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := tt.gen("idx", 20, end)
			assert.Len(t, docs, 20)
			assert.Equal(t, docs, tt.gen("idx", 20, end), "must be deterministic")
			for i, doc := range docs {
				for _, f := range tt.fields {
					assert.Contains(t, doc, f)
				}
				ts, err := time.Parse(time.RFC3339, doc["@timestamp"].(string))
				assert.NoError(t, err)
				assert.Equal(t, end.Add(-time.Duration(19-i)*tt.step), ts)
			}
		})
	}

	t.Run("forum join", func(t *testing.T) {
		docs := forum("idx", 8, end)
		for i, doc := range docs {
			if i%4 == 0 {
				assert.Equal(t, "question", doc["qa"])
				assert.NotEmpty(t, doc["comments"])
				continue
			}
			assert.Equal(t, map[string]any{"name": "answer", "parent": docID("idx", i/4*4)}, doc["qa"])
		}
	})

	t.Run("events amount", func(t *testing.T) {
		for _, doc := range events("idx", 200, end) {
			switch doc["action"] {
			case "purchase", "refund":
				assert.Positive(t, doc["amount"])
			default:
				assert.Zero(t, doc["amount"])
			}
		}
	})
}

func TestParseEnd(t *testing.T) {
	now := time.Date(2026, 11, 4, 8, 15, 42, 0, time.FixedZone("PST", -8*3600))
	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{"empty uses now", "", time.Date(2026, 11, 4, 16, 15, 0, 0, time.UTC), false},
		{"utc", "2026-10-30T19:39:00Z", time.Date(2026, 10, 30, 19, 39, 0, 0, time.UTC), false},
		{"offset", "2026-10-30T12:39:00-07:00", time.Date(2026, 10, 30, 19, 39, 0, 0, time.UTC), false},
		{"invalid", "2026-10-30", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEnd(tt.in, now)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, time.UTC, got.Location())
		})
	}
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name  string
		index string
		want  string
	}{
		{"invalid json", `{"mappings":`, "index TestCreate.invalid_json:"},
		{"invalid storage", `{"storage_type": "bogus"}`, "index TestCreate.invalid_storage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := "TestCreate." + strings.ReplaceAll(tt.name, " ", "_")
			err := create(name, dataset{index: tt.index})
			assert.ErrorContains(t, err, tt.want)
			_, ok := core.GetIndex(name)
			assert.False(t, ok)
		})
	}
}

func TestSeed(t *testing.T) {
	t.Run("missing index", func(t *testing.T) {
		assert.EqualError(t, seed("TestSeed.missing", nil), "index TestSeed.missing not found")
	})

	name := "TestSeed.index_1"
	require.NoError(t, create(name, dataset{index: `{"mappings": {"properties": {"n": {"type": "integer"}}}}`}))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })

	t.Run("rejected document", func(t *testing.T) {
		docs := []map[string]any{{"n": "not a number", "@timestamp": "2026-10-30T19:39:00Z"}}
		assert.ErrorContains(t, seed(name, docs), "index "+name+" doc 1:")
	})

	t.Run("stalled indexing times out", func(t *testing.T) {
		old := stall
		stall = 300 * time.Millisecond
		t.Cleanup(func() { stall = old })
		// stored as UTC, so the offset stamp never matches and progress stalls
		docs := []map[string]any{{"n": 1, "@timestamp": "2026-10-30T12:39:00-07:00"}}
		assert.EqualError(t, seed(name, docs), "index "+name+": 0/1 docs indexed before timeout")
	})
}

func TestRunErrors(t *testing.T) {
	end := time.Date(2026, 10, 30, 19, 39, 0, 0, time.UTC)
	gen := func(string, int, time.Time) []map[string]any {
		return []map[string]any{{"n": "not a number", "@timestamp": end.Format(time.RFC3339)}}
	}
	tests := []struct {
		name string
		d    dataset
		want string
	}{
		{"create", dataset{"bad", `{"storage_type": "bogus"}`, gen}, "index TestRunErrors.create.bad:"},
		{"seed", dataset{"bad", `{"mappings": {"properties": {"n": {"type": "integer"}}}}`, gen}, "index TestRunErrors.seed.bad doc 1:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := datasets
			datasets = []dataset{tt.d}
			t.Cleanup(func() { datasets = old })
			prefix := "TestRunErrors." + tt.name + "."
			assert.ErrorContains(t, run(prefix, map[string]int{"bad": 1}, end, true), tt.want)
			if _, ok := core.GetIndex(prefix + "bad"); ok {
				assert.NoError(t, core.DeleteIndex(prefix+"bad"))
			}
		})
	}
}

func TestRun(t *testing.T) {
	prefix := "TestRun.mock_"
	end := time.Now().UTC().Truncate(time.Minute)
	counts := map[string]int{"products": 5, "logs": 7, "stores": 6, "forum": 9, "events": 2500}
	assert.NoError(t, run(prefix, counts, end, false))
	// rerun upserts the same ids instead of duplicating, and returns only once
	// the updated timestamps are searchable
	assert.NoError(t, run(prefix, counts, end.Add(time.Minute), false))
	// reset recreates the indexes from scratch
	assert.NoError(t, run(prefix, counts, end.Add(time.Minute), true))

	search := func(t *testing.T, name, body string) *meta.SearchResponse {
		index, ok := core.ZED_INDEX_LIST.Get(name)
		assert.True(t, ok)
		query := &meta.ZedQuery{Size: 100}
		require.NoError(t, json.Unmarshal([]byte(body), query))
		res, err := index.Search(query)
		require.NoError(t, err)
		return res
	}

	for _, d := range datasets {
		name := prefix + d.name
		t.Run(name, func(t *testing.T) {
			res := search(t, name, `{"query": {"match_all": {}}}`)
			assert.Equal(t, counts[d.name], res.Hits.Total.Value)
			index, _ := core.ZED_INDEX_LIST.Get(name)
			var stamps []string
			for _, doc := range d.gen(name, counts[d.name], end.Add(time.Minute)) {
				stamps = append(stamps, doc["@timestamp"].(string))
			}
			done, err := indexed(index, name, stamps)
			assert.NoError(t, err)
			assert.Equal(t, counts[d.name], done)
		})
	}

	t.Run("features", func(t *testing.T) {
		logsIndex, _ := core.ZED_INDEX_LIST.Get(prefix + "logs")
		assert.Equal(t, meta.IndexTypeLogs, logsIndex.GetIndexType())
		prop, ok := logsIndex.GetMappings().GetProperty("service.name")
		assert.True(t, ok)
		assert.Equal(t, "keyword", prop.Type)

		products, _ := core.ZED_INDEX_LIST.Get(prefix + "products")
		prop, _ = products.GetMappings().GetProperty("embedding")
		assert.Equal(t, "vector", prop.Type)
		assert.Equal(t, 4, prop.Dims)

		res := search(t, prefix+"stores", `{"query": {"geo_distance": {"distance": "500km", "location": {"lat": 37.7749, "lon": -122.4194}}}}`)
		assert.Equal(t, counts["stores"], res.Hits.Total.Value)

		res = search(t, prefix+"forum", `{"query": {"nested": {"path": "comments", "query": {"range": {"comments.stars": {"gte": 1}}}}}}`)
		assert.Equal(t, 3, res.Hits.Total.Value) // questions 1, 5, 9

		res = search(t, prefix+"forum", `{"query": {"has_parent": {"parent_type": "question", "query": {"match_all": {}}}}}`)
		assert.Equal(t, 6, res.Hits.Total.Value)

		// events spans multiple indexed() batches
		purchases := 0
		for _, doc := range events(prefix+"events", counts["events"], end) {
			if doc["action"] == "purchase" {
				purchases++
			}
		}
		res = search(t, prefix+"events", `{"query": {"term": {"action": "purchase"}}}`)
		assert.Equal(t, purchases, res.Hits.Total.Value)
	})

	for _, d := range datasets {
		assert.NoError(t, core.DeleteIndex(prefix+d.name))
	}
}
