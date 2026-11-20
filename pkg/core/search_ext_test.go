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
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestScoreSorted(t *testing.T) {
	tests := []struct {
		name string
		sort interface{}
		want bool
	}{
		{"none", nil, true},
		{"score string", "_score", true},
		{"score array", []interface{}{"-_score"}, true},
		{"score object desc", []interface{}{map[string]interface{}{"_score": "desc"}}, true},
		{"score object order", []interface{}{map[string]interface{}{"_score": map[string]interface{}{"order": "desc"}}}, true},
		{"score asc", []interface{}{map[string]interface{}{"_score": "asc"}}, false},
		{"field", "-@timestamp", false},
		{"score then field", []interface{}{"_score", map[string]interface{}{"year": "desc"}}, false},
		{"bad type", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scoreSorted(tt.sort))
		})
	}
}

func newRescoreIndex(t *testing.T, name string) *Index {
	t.Helper()
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	require.NoError(t, index.CreateDocument("a", map[string]interface{}{"title": "fox fox fox", "tag": "x"}, false))
	require.NoError(t, index.CreateDocument("b", map[string]interface{}{"title": "fox", "tag": "y"}, false))
	waitForDocs(t, index, 2)
	return index
}

func rescoreQuery(rescore interface{}) *meta.ZedQuery {
	return &meta.ZedQuery{
		Query:   map[string]interface{}{"match": map[string]interface{}{"title": "fox"}},
		Size:    10,
		Rescore: rescore,
	}
}

func TestSearch_Rescore(t *testing.T) {
	index := newRescoreIndex(t, "TestSearch_Rescore.index_1")

	t.Run("hits past the window are reordered too", func(t *testing.T) {
		q := rescoreQuery(map[string]interface{}{
			"window_size": 1,
			"query": map[string]interface{}{
				"rescore_query":        map[string]interface{}{"term": map[string]interface{}{"tag": "x"}},
				"rescore_query_weight": 0.0001,
				"score_mode":           "multiply",
			},
		})
		resp, err := index.Search(q)
		require.NoError(t, err)
		require.Len(t, resp.Hits.Hits, 2)
		assert.Equal(t, "b", resp.Hits.Hits[0].ID, "the rescored top hit drops below the next hit")
		assert.Equal(t, 10, q.Size)
		assert.Equal(t, 0, q.From)
	})

	t.Run("field sort is rejected", func(t *testing.T) {
		q := rescoreQuery(map[string]interface{}{"query": map[string]interface{}{"rescore_query": map[string]interface{}{"match_all": map[string]interface{}{}}}})
		q.Sort = "-title"
		_, err := index.Search(q)
		assert.ErrorContains(t, err, "[rescore] cannot be used with a [sort]")
	})

	t.Run("window over max results is rejected", func(t *testing.T) {
		maxResults := config.Global.MaxResults
		config.Global.MaxResults = 100
		t.Cleanup(func() { config.Global.MaxResults = maxResults })
		q := rescoreQuery(map[string]interface{}{"query": map[string]interface{}{"rescore_query": map[string]interface{}{"match_all": map[string]interface{}{}}}})
		q.From = 95
		_, err := index.Search(q)
		assert.ErrorContains(t, err, "must be less than or equal to [100] but was [105]")
		assert.Equal(t, 95, q.From)
		assert.Equal(t, 10, q.Size)
	})

	t.Run("overflowing or negative page is rejected", func(t *testing.T) {
		for _, page := range [][2]int{{math.MaxInt, math.MaxInt}, {-1, 10}, {0, -1}, {1, math.MaxInt}} {
			q := rescoreQuery(map[string]interface{}{"query": map[string]interface{}{"rescore_query": map[string]interface{}{"match_all": map[string]interface{}{}}}})
			q.From, q.Size = page[0], page[1]
			_, err := index.Search(q)
			assert.ErrorContains(t, err, "[rescore] from and size must be within")
			assert.Equal(t, page[0], q.From)
			assert.Equal(t, page[1], q.Size)
		}
	})

	t.Run("page is restored after a failed rescore query", func(t *testing.T) {
		q := rescoreQuery(map[string]interface{}{"window_size": 50, "query": map[string]interface{}{"rescore_query": map[string]interface{}{"bogus": map[string]interface{}{}}}})
		q.From = 1
		_, err := index.Search(q)
		assert.Error(t, err)
		assert.Equal(t, 1, q.From)
		assert.Equal(t, 10, q.Size)

		_, err = MultiSearch([]string{"TestSearch_Rescore.index_1"}, q)
		assert.Error(t, err)
		assert.Equal(t, 1, q.From)
		assert.Equal(t, 10, q.Size)
	})
}

func TestMultiSearch_SuggestWithoutReaders(t *testing.T) {
	name := "TestMultiSearch_SuggestWithoutReaders.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	// a time range outside every shard leaves the matched index without readers
	timed := map[string]interface{}{"range": map[string]interface{}{"@timestamp": map[string]interface{}{
		"gte": "1970-01-01T00:00:00Z", "lte": "1970-01-02T00:00:00Z", "format": "2006-01-02T15:04:05Z07:00",
	}}}
	tests := []struct {
		name    string
		suggest map[string]interface{}
		wantErr bool
	}{
		{name: "named entry", suggest: map[string]interface{}{"s": map[string]interface{}{"text": "fox", "term": map[string]interface{}{"field": "title"}}}},
		{name: "malformed", suggest: map[string]interface{}{"s": "bad"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := MultiSearch([]string{name}, &meta.ZedQuery{Query: timed, Size: 10, Suggest: tt.suggest})
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, resp.Suggest, "s")
		})
	}
	_, err = MultiSearch([]string{"TestMultiSearch_SuggestWithoutReaders.missing"}, &meta.ZedQuery{Size: 10})
	assert.ErrorContains(t, err, "no index found")
}

func TestSearchExt_FinishHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name  string
		query *meta.ZedQuery
	}{
		{name: "suggest", query: &meta.ZedQuery{Suggest: map[string]interface{}{"s": "bad"}}},
		{name: "no extension", query: &meta.ZedQuery{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := &searchExt{query: tt.query}
			assert.ErrorIs(t, ext.finish(ctx, &meta.SearchResponse{}), context.Canceled)
		})
	}
}
