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
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/bluge/directory"
	zcluster "github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestInternalSearch(t *testing.T) {
	indexName := "TestInternalSearch.index_1"
	index, err := core.NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(indexName)) })

	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "single index",
			body:     `{"indexes":["` + indexName + `"],"query":{"query":{"match_all":{}},"size":1}}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "index pattern",
			body:     `{"indexes":["TestInternalSearch.*"],"query":{"query":{"match_all":{}},"size":1}}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "missing query falls back to the default",
			body:     `{"indexes":["` + indexName + `"]}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "unknown index",
			body:     `{"indexes":["TestInternalSearch.missing"],"query":{"query":{"match_all":{}}}}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing index in a batch is an error",
			body:     `{"indexes":["` + indexName + `","TestInternalSearch.missing"]}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "metadata alone does not make a disk replica readable",
			body:     `{"indexes":["` + indexName + `"],"replica":true}`,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "malformed body",
			body:     `{"indexes":`,
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.body)
			InternalSearch(c)
			require.Equal(t, tt.wantCode, w.Code, w.Body.String())

			if tt.wantCode != http.StatusOK {
				return
			}
			resp := new(meta.SearchResponse)
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
			assert.Equal(t, 0, resp.Hits.Total.Value, "the index is empty")
		})
	}
}

// searchableIndex creates a disk index holding docs and waits until a
// search sees them.
func searchableIndex(t *testing.T, name string, docs map[string]map[string]interface{}) *core.Index {
	t.Helper()
	index, err := core.NewIndex(name, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	index.GetMappings().SetProperty("label", meta.NewProperty("keyword"))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })
	for id, doc := range docs {
		require.NoError(t, index.CreateDocument(id, doc, false))
	}
	require.Eventually(t, func() bool {
		resp, err := index.Search(&meta.ZedQuery{Size: 1})
		return err == nil && resp.Hits.Total.Value == len(docs)
	}, 5*time.Second, 20*time.Millisecond)
	return index
}

// The owners order a keyword by its bytes, also when it looks like a date:
// the merge follows them with the sort keys it asks for, and reads the
// values as dates from _source without.
func TestInternalSearchTopHitsUsesOwnerSortKeys(t *testing.T) {
	labels := []struct{ id, label string }{
		{id: "left", label: "2024-01-01T10:00:00+02:00"}, // the earlier time, the later bytes
		{id: "right", label: "2024-01-01T09:00:00Z"},
		{id: "none"},
	}
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		doc := map[string]interface{}{"other": "x"}
		if l.label != "" {
			doc["label"] = l.label
		}
		name := "TestInternalSearchTopHitsUsesOwnerSortKeys." + l.id
		searchableIndex(t, name, map[string]map[string]interface{}{l.id: doc})
		names = append(names, name)
	}
	asc := []interface{}{map[string]interface{}{"label": "asc"}}
	desc := []interface{}{map[string]interface{}{"label": "desc"}}

	tests := []struct {
		name     string
		sort     interface{}
		sortKeys bool
		wantKeys bool // the owners answer with _sort_key
		want     []string
	}{
		{name: "ascending by key", sort: asc, sortKeys: true, wantKeys: true, want: []string{"right", "left", "none"}},
		{name: "descending by key", sort: desc, sortKeys: true, wantKeys: true, want: []string{"left", "right", "none"}},
		{name: "field name", sort: "label", sortKeys: true, wantKeys: true, want: []string{"right", "left", "none"}},
		{name: "ascending without keys", sort: asc, want: []string{"left", "right", "none"}},
		{name: "descending without keys", sort: desc, want: []string{"right", "left", "none"}},
		{name: "score order has no key", sort: "_score", sortKeys: true, want: []string{"left", "none", "right"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := meta.Aggregations{TopHits: &meta.AggregationTopHits{Size: 3, Sort: tt.sort, Source: false}}
			query := &meta.ZedQuery{Aggregations: map[string]meta.Aggregations{
				"top": top,
				"by": {
					Terms:        &meta.AggregationsTerms{Field: "other"},
					Aggregations: map[string]meta.Aggregations{"top": top},
				},
			}}
			responses := make([]*meta.SearchResponse, 0, len(names))
			for _, name := range names {
				payload, err := json.Marshal(InternalSearchRequest{
					Indexes: []string{name}, SortKeys: tt.sortKeys, Query: zcluster.ScatterQuery(query),
				})
				require.NoError(t, err)
				c, w := utils.NewContext()
				utils.SetRequestData(c, string(payload))
				InternalSearch(c)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				assert.Equal(t, tt.wantKeys, strings.Contains(w.Body.String(), "_sort_key"), w.Body.String())
				if tt.wantKeys {
					assert.Equal(t, 2, strings.Count(w.Body.String(), "_sort_key"), "top_hits in buckets carry keys too")
				}
				resp, err := zcluster.DecodeSearchResponse(w.Body.Bytes())
				require.NoError(t, err)
				responses = append(responses, resp)
			}

			got := zcluster.MergeSearch(responses, query)
			hits := got.Aggregations["top"].Hits
			require.NotNil(t, hits)
			ids := make([]string, 0, len(hits.Hits))
			for _, hit := range hits.Hits {
				ids = append(ids, hit.ID)
				assert.Empty(t, hit.Source)
			}
			assert.Equal(t, tt.want, ids)

			data, err := json.Marshal(got)
			require.NoError(t, err)
			assert.NotContains(t, string(data), "_sort_key", "the keys stay between the nodes")
			var nested struct {
				Aggregations struct {
					By struct {
						Buckets []struct {
							Top struct {
								Hits struct {
									Hits []struct {
										ID string `json:"_id"`
									} `json:"hits"`
								} `json:"hits"`
							} `json:"top"`
						} `json:"buckets"`
					} `json:"by"`
				} `json:"aggregations"`
			}
			require.NoError(t, json.Unmarshal(data, &nested))
			require.Len(t, nested.Aggregations.By.Buckets, 1)
			ids = ids[:0]
			for _, hit := range nested.Aggregations.By.Buckets[0].Top.Hits.Hits {
				ids = append(ids, hit.ID)
			}
			assert.Equal(t, tt.want, ids, "in a bucket")
		})
	}
}

// A node asked as a replica answers only from copies it may serve: a cold
// index, read-only on object storage, and never a disk index it merely
// knows the metadata of.
func TestInternalSearchReplica(t *testing.T) {
	store := directory.NewMemoryObjectStore()
	restore, err := directory.SetTestObjectStore(core.StorageTypeS3, store)
	require.NoError(t, err)
	oldBucket, oldCold := config.Global.S3.Bucket, config.Global.Tier.ColdStorageType
	config.Global.S3.Bucket = "zed-test"
	config.Global.Tier.ColdStorageType = core.StorageTypeS3
	t.Cleanup(func() {
		config.Global.S3.Bucket, config.Global.Tier.ColdStorageType = oldBucket, oldCold
		restore()
	})

	const cold, disk = "TestInternalSearchReplica.cold", "TestInternalSearchReplica.disk"
	doc := map[string]map[string]interface{}{"1": {"label": "a"}}
	searchableIndex(t, cold, doc)
	searchableIndex(t, disk, doc)
	require.NoError(t, core.MoveIndexTier(cold, core.TierCold))

	list := func(names ...string) string { return `["` + strings.Join(names, `","`) + `"]` }
	tests := []struct {
		name      string
		body      string
		wantCode  int
		wantTotal int
		wantError string
	}{
		{name: "cold index", body: `{"indexes":` + list(cold) + `,"replica":true}`, wantCode: http.StatusOK, wantTotal: 1},
		{
			name: "disk index without an installed copy", body: `{"indexes":` + list(disk) + `,"replica":true}`,
			wantCode: http.StatusNotFound, wantError: "index " + disk + " is not installed on this replica",
		},
		{
			name: "a batch is served whole or not at all", body: `{"indexes":` + list(cold, disk) + `,"replica":true}`,
			wantCode: http.StatusNotFound, wantError: "index " + disk + " is not installed on this replica",
		},
		{
			name: "unknown index", body: `{"indexes":` + list("TestInternalSearchReplica.missing") + `,"replica":true}`,
			wantCode: http.StatusNotFound, wantError: "is not installed on this replica",
		},
		{
			name: "a pattern names no copy", body: `{"indexes":` + list("TestInternalSearchReplica.*") + `,"replica":true}`,
			wantCode: http.StatusNotFound, wantError: "is not installed on this replica",
		},
		{name: "the owner serves both", body: `{"indexes":` + list(cold, disk) + `}`, wantCode: http.StatusOK, wantTotal: 2},
		{name: "the owner serves a pattern", body: `{"indexes":` + list("TestInternalSearchReplica.*") + `}`, wantCode: http.StatusOK, wantTotal: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.body)
			InternalSearch(c)
			require.Equal(t, tt.wantCode, w.Code, w.Body.String())
			if tt.wantCode != http.StatusOK {
				assert.Contains(t, w.Body.String(), tt.wantError)
				return
			}
			resp := new(meta.SearchResponse)
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
			assert.Equal(t, tt.wantTotal, resp.Hits.Total.Value)
		})
	}
}

func TestInternalIndexes(t *testing.T) {
	indexName := "TestInternalIndexes.index_1"
	index, err := core.NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(indexName)) })

	c, w := utils.NewContext()
	InternalIndexes(c)
	require.Equal(t, http.StatusOK, w.Code)

	resp := new(InternalIndexesResponse)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))

	var found *IndexOwner
	for i := range resp.Indexes {
		if resp.Indexes[i].Name == indexName {
			found = &resp.Indexes[i]
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "disk", found.StorageType)
	assert.Equal(t, zcluster.PartitionOf(indexName), found.Partition)
	assert.True(t, found.Local, "without clustering every index is local")
	assert.Empty(t, found.Owner)
}

func TestInternalIndexesCluster(t *testing.T) {
	local, remote := "TestInternalIndexesCluster.index_a", "TestInternalIndexesCluster.index_b"
	for _, name := range []string{remote, local} {
		index, err := core.NewIndex(name, "disk", 1)
		require.NoError(t, err)
		require.NoError(t, core.StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })
	}
	startTestCluster(t, map[string]string{local: "node-a", remote: "node-b"})

	c, w := utils.NewContext()
	InternalIndexes(c)
	require.Equal(t, http.StatusOK, w.Code)

	resp := new(InternalIndexesResponse)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
	assert.Equal(t, "node-a", resp.Node)
	var got []IndexOwner
	for _, index := range resp.Indexes {
		if index.Name == local || index.Name == remote {
			got = append(got, index)
		}
	}
	assert.Equal(t, []IndexOwner{
		{Name: local, StorageType: "disk", Partition: zcluster.PartitionOf(local), Owner: "node-a", Local: true},
		{Name: remote, StorageType: "disk", Partition: zcluster.PartitionOf(remote), Owner: "node-b", Local: false},
	}, got, "sorted by name")
}

func TestAddPartialAggregations(t *testing.T) {
	avgDef := map[string]meta.Aggregations{
		"avg_n": {Avg: &meta.AggregationMetric{Field: "n"}},
		"max_n": {Max: &meta.AggregationMetric{Field: "n"}},
	}

	tests := []struct {
		name        string
		resp        *meta.SearchResponse
		wantCount   uint64
		wantSum     float64
		wantPartial bool
	}{
		{
			name: "avg gets its counters",
			resp: &meta.SearchResponse{
				Hits: meta.Hits{Total: meta.Total{Value: 4}},
				Aggregations: map[string]meta.AggregationResponse{
					"avg_n": {Value: float64(5)},
				},
			},
			wantPartial: true, wantCount: 4, wantSum: 20,
		},
		{
			name: "other aggregations are untouched",
			resp: &meta.SearchResponse{
				Hits: meta.Hits{Total: meta.Total{Value: 4}},
				Aggregations: map[string]meta.AggregationResponse{
					"max_n": {Value: float64(5)},
				},
			},
		},
		{
			name: "avg without a numeric value is untouched",
			resp: &meta.SearchResponse{
				Hits: meta.Hits{Total: meta.Total{Value: 4}},
				Aggregations: map[string]meta.AggregationResponse{
					"avg_n": {Value: nil},
				},
			},
		},
		{
			name: "no aggregations at all",
			resp: &meta.SearchResponse{Hits: meta.Hits{Total: meta.Total{Value: 4}}},
		},
		{
			name: "nil response is safe",
			resp: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addPartialAggregations(tt.resp, &meta.ZedQuery{Aggregations: avgDef})
			if tt.resp == nil {
				return
			}
			for name, agg := range tt.resp.Aggregations {
				partial, ok := agg.Aggregations[zcluster.PartialAggName]
				if name != "avg_n" || !tt.wantPartial {
					assert.False(t, ok, name)
					continue
				}
				require.True(t, ok)
				require.NotNil(t, partial.Count)
				require.NotNil(t, partial.Sum)
				assert.Equal(t, tt.wantCount, *partial.Count)
				assert.InDelta(t, tt.wantSum, *partial.Sum, 0.0001)
			}
		})
	}
}
