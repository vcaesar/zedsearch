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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zcluster "github.com/vcaesar/zedsearch/pkg/cluster"
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

func TestInternalSearchTopHitsUsesOwnerSortKeys(t *testing.T) {
	const left = "2024-01-01T10:00:00+02:00"
	const right = "2024-01-01T09:00:00Z"
	query := &meta.ZedQuery{Aggregations: map[string]meta.Aggregations{
		"top": {TopHits: &meta.AggregationTopHits{Size: 1, Sort: "label", Source: false}},
	}}
	var responses []*meta.SearchResponse
	for id, value := range map[string]string{"left": left, "right": right} {
		name := "TestInternalSearchTopHitsUsesOwnerSortKeys." + id
		index, err := core.NewIndex(name, "disk", 1)
		require.NoError(t, err)
		require.NoError(t, core.StoreIndex(index))
		index.GetMappings().SetProperty("label", meta.NewProperty("keyword"))
		t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })
		require.NoError(t, index.CreateDocument(id, map[string]interface{}{"label": value}, false))
		require.Eventually(t, func() bool {
			resp, err := index.Search(&meta.ZedQuery{Size: 1})
			return err == nil && resp.Hits.Total.Value == 1
		}, 5*time.Second, 20*time.Millisecond)

		payload, err := json.Marshal(InternalSearchRequest{Indexes: []string{name}, Query: zcluster.ScatterQuery(query)})
		require.NoError(t, err)
		c, w := utils.NewContext()
		utils.SetRequestData(c, string(payload))
		InternalSearch(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp, err := zcluster.DecodeSearchResponse(w.Body.Bytes())
		require.NoError(t, err)
		responses = append(responses, resp)
	}
	got := zcluster.MergeSearch(responses, query).Aggregations["top"].Hits
	require.NotNil(t, got)
	require.Len(t, got.Hits, 1)
	assert.Equal(t, "right", got.Hits[0].ID, "keyword sorts by bytes even when its value looks like a date")
	assert.Empty(t, got.Hits[0].Source)
	assert.Nil(t, got.Hits[0].SortKey)
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
