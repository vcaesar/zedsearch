/* Copyright 2022 Zinc Labs Inc. and Contributors
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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestMultiSearch(t *testing.T) {
	type args struct {
		indexName string
		query     *meta.ZedQuery
	}
	tests := []struct {
		name    string
		args    args
		want    *meta.SearchResponse
		wantErr bool
	}{
		{
			name: "multiple search",
			args: args{
				indexName: "",
				query: &meta.ZedQuery{
					Query: &meta.Query{
						MatchAll: &meta.MatchAllQuery{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "wildcard search",
			args: args{
				indexName: "TestMultiSearch.*",
				query: &meta.ZedQuery{
					Query: &meta.Query{
						MatchAll: &meta.MatchAllQuery{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty",
			args: args{
				indexName: "TestMultiSearchNotExist",
				query: &meta.ZedQuery{
					Query: &meta.Query{
						MatchAll: &meta.MatchAllQuery{},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "timeout",
			args: args{
				indexName: "",
				query: &meta.ZedQuery{
					Query: &meta.Query{
						MatchAll: &meta.MatchAllQuery{},
					},
					Timeout: 1,
				},
			},
			wantErr: false,
		},
	}

	indexNames := []string{"TestMultiSearch.index_1", "TestMultiSearch.index_2"}
	t.Run("prepare", func(t *testing.T) {
		for _, indexName := range indexNames {
			index, err := NewIndex(indexName, "disk", 2)
			assert.NoError(t, err)
			assert.NotNil(t, index)

			err = StoreIndex(index)
			assert.NoError(t, err)
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			var got *meta.SearchResponse
			if tt.args.indexName == "" {
				got, err = MultiSearch(nil, tt.args.query)
			} else {
				got, err = MultiSearch([]string{tt.args.indexName}, tt.args.query)
			}
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}

	t.Run("cleanup", func(t *testing.T) {
		for _, indexName := range indexNames {
			err := DeleteIndex(indexName)
			assert.NoError(t, err)
		}
	})
}

func TestIsMatchIndex(t *testing.T) {
	ret := isMatchIndex("abc", "a") //  false
	assert.False(t, ret)
	ret = isMatchIndex("abc", "a*") // true
	assert.True(t, ret)
	ret = isMatchIndex("abc", "*bc") // true
	assert.True(t, ret)
	ret = isMatchIndex("abc", "bc") // false
	assert.False(t, ret)
	ret = isMatchIndex("abc", "abc") // true
	assert.True(t, ret)
}

// A search over several indexes expands default fields and restores top_hits
// vectors with the mappings of every index, not of the first one listed.
func TestMultiSearch_UsesEveryIndexMappings(t *testing.T) {
	exclude := true
	names := []string{"TestMultiSearch_UsesEveryIndexMappings.index_a", "TestMultiSearch_UsesEveryIndexMappings.index_b"}
	docs := []map[string]interface{}{
		{"title": "quick fox"},
		{"summary": "lazy fox", "emb": []interface{}{1.0, 2.0}},
	}
	for i, name := range names {
		index, err := NewIndex(name, "disk", 1)
		require.NoError(t, err)
		require.NoError(t, index.SetSettings(&meta.IndexSettings{ExcludeSourceVectors: &exclude}))
		require.NoError(t, StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
		index.GetMappings().SetAllEnabled(false)
		// the first index by name maps emb as text, hiding the vector of the second
		if i == 0 {
			index.GetMappings().SetProperty("emb", meta.NewProperty("text"))
		} else {
			index.GetMappings().SetProperty("emb", meta.NewProperty("vector"))
		}
		require.NoError(t, index.CreateDocument("1", docs[i], false))
		waitForDocs(t, index, 1)
	}

	resp, err := MultiSearch([]string{"TestMultiSearch_UsesEveryIndexMappings.*"}, &meta.ZedQuery{
		Query:        &meta.Query{QueryString: &meta.QueryStringQuery{Query: "fox"}},
		Size:         10,
		Aggregations: map[string]meta.Aggregations{"top": {TopHits: &meta.AggregationTopHits{Size: 10}}},
	})
	require.NoError(t, err)
	found := map[string]interface{}{}
	for _, hit := range resp.Hits.Hits {
		found[hit.Index] = hit.Source
	}
	require.Len(t, found, 2, "fox is in a field only one of the indexes maps")
	assert.Equal(t, []interface{}{1.0, 2.0}, found[names[1]].(map[string]interface{})["emb"])

	top := resp.Aggregations["top"].Hits
	require.NotNil(t, top)
	require.Len(t, top.Hits, 2)
	topByIndex := map[string]interface{}{}
	for _, hit := range top.Hits {
		topByIndex[hit.Index] = hit.Source
	}
	require.Contains(t, topByIndex, names[1], "top_hits holds the hit of the vector index")
	assert.Equal(t, []interface{}{1.0, 2.0}, topByIndex[names[1]].(map[string]interface{})["emb"])
}

func TestUnionMappings(t *testing.T) {
	a, b := &Index{ref: &meta.Index{Name: "a", Mappings: meta.NewMappings()}}, &Index{ref: &meta.Index{Name: "b", Mappings: meta.NewMappings()}}
	a.ref.Mappings.SetProperty("body", meta.Property{Type: "text", Index: true})
	a.ref.Mappings.SetAllEnabled(true)
	b.ref.Mappings.SetProperty("body", meta.Property{Type: "keyword", Index: true})
	b.ref.Mappings.SetProperty("emb", meta.NewProperty("vector"))
	b.ref.Mappings.SetAllEnabled(false)

	t.Run("one index keeps its own", func(t *testing.T) {
		m, _ := unionMappings(map[string]*Index{"a": a})
		assert.Same(t, a.ref.Mappings, m)
	})
	t.Run("union", func(t *testing.T) {
		m, _ := unionMappings(map[string]*Index{"b": b, "a": a})
		body, _ := m.GetProperty("body")
		assert.Equal(t, "text", body.Type, "the first index by name wins")
		assert.Equal(t, []string{"emb"}, m.VectorFields())
		assert.False(t, m.AllEnabled(), "_all is searched only when every index has it")
	})
	t.Run("vector hidden by an earlier mapping", func(t *testing.T) {
		text := &Index{ref: &meta.Index{Name: "a_text", Mappings: meta.NewMappings()}}
		text.ref.Mappings.SetProperty("emb", meta.NewProperty("text"))
		m, _ := unionMappings(map[string]*Index{"b": b, "a_text": text})
		emb, _ := m.GetProperty("emb")
		assert.Equal(t, "text", emb.Type, "the query keeps the first index by name")
		assert.Equal(t, []string{"emb"}, m.VectorFields(), "the vector of b is still restored")
	})
}
