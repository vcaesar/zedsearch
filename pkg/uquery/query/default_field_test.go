/* Copyright 2026 AtomAI. and Contributors
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

package query

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func defaultFieldMappings(all bool) *meta.Mappings {
	m := meta.NewMappings()
	m.SetProperty("title", meta.NewProperty("text"))
	m.SetProperty("tag", meta.NewProperty("keyword"))
	m.SetProperty("price", meta.NewProperty("numeric"))
	m.SetProperty("@timestamp", meta.NewProperty("date"))
	m.SetProperty("_id", meta.NewProperty("keyword"))
	m.SetAllEnabled(all)
	return m
}

// defaultFieldReader indexes docs without an _all field.
func defaultFieldReader(t *testing.T) *riot.Reader {
	w, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, w.Close()) })
	docs := []*riot.Document{
		riot.NewDocument("1").AddField(riot.NewTextField("title", "quick brown fox").SearchTermPositions()).
			AddField(riot.NewKeywordField("tag", "Red Fox")).AddField(riot.NewNumericField("price", 10)),
		riot.NewDocument("2").AddField(riot.NewTextField("title", "lazy dog").SearchTermPositions()).
			AddField(riot.NewKeywordField("tag", "dog")).AddField(riot.NewNumericField("price", 20)),
	}
	for _, d := range docs {
		require.NoError(t, w.Insert(d))
	}
	r, err := w.Reader()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	return r
}

func countHits(t *testing.T, r *riot.Reader, q riot.Query) uint64 {
	dmi, err := r.Search(context.Background(), riot.NewTopNSearch(10, q).WithStandardAggregations())
	require.NoError(t, err)
	return dmi.Aggregations().Count()
}

func TestQueryStringDefaultFields(t *testing.T) {
	r := defaultFieldReader(t)
	tests := []struct {
		name  string
		query map[string]interface{}
		want  uint64
	}{
		{name: "term", query: map[string]interface{}{"query": "fox"}, want: 1},
		{name: "two terms", query: map[string]interface{}{"query": "fox dog"}, want: 2},
		{name: "phrase", query: map[string]interface{}{"query": `"brown fox"`}, want: 1},
		{name: "keyword whole value", query: map[string]interface{}{"query": `"Red Fox"`}, want: 1},
		{name: "wildcard", query: map[string]interface{}{"query": "laz*"}, want: 1},
		{name: "number", query: map[string]interface{}{"query": "20"}, want: 1},
		{name: "numeric range", query: map[string]interface{}{"query": "price:>15"}, want: 1},
		{name: "must not", query: map[string]interface{}{"query": "fox -brown"}, want: 0},
		{name: "fielded term", query: map[string]interface{}{"query": "title:dog"}, want: 1},
		{name: "default_field", query: map[string]interface{}{"query": "dog", "default_field": "tag"}, want: 1},
		{name: "default_field excludes", query: map[string]interface{}{"query": "lazy", "default_field": "tag"}, want: 0},
		{name: "fields", query: map[string]interface{}{"query": "fox", "fields": []interface{}{"title"}}, want: 1},
		{name: "no match", query: map[string]interface{}{"query": "cat"}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := QueryStringQuery(tt.query, defaultFieldMappings(false), nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, countHits(t, r, q))
		})
	}

	t.Run("_all enabled searches _all", func(t *testing.T) {
		q, err := QueryStringQuery(map[string]interface{}{"query": "fox"}, defaultFieldMappings(true), nil)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), countHits(t, r, q), "the docs have no _all field")
	})
	t.Run("no default fields", func(t *testing.T) {
		q, err := QueryStringQuery(map[string]interface{}{"query": "fox"}, meta.NewMappings(), nil)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), countHits(t, r, q))
	})
}

func TestQueryAllFieldFallback(t *testing.T) {
	r := defaultFieldReader(t)
	tests := []struct {
		name  string
		query map[string]interface{}
		want  uint64
	}{
		{name: "match", query: map[string]interface{}{"match": map[string]interface{}{"_all": "dog"}}, want: 1},
		{name: "match_phrase", query: map[string]interface{}{"match_phrase": map[string]interface{}{"_all": "brown fox"}}, want: 1},
		{name: "prefix", query: map[string]interface{}{"prefix": map[string]interface{}{"_all": "qui"}}, want: 1},
		{name: "wildcard", query: map[string]interface{}{"wildcard": map[string]interface{}{"_all": "do*"}}, want: 1},
		{name: "term", query: map[string]interface{}{"term": map[string]interface{}{"_all": "lazy"}}, want: 1},
		{name: "bool", query: map[string]interface{}{"bool": map[string]interface{}{"should": []interface{}{
			map[string]interface{}{"match": map[string]interface{}{"_all": "dog"}},
			map[string]interface{}{"match": map[string]interface{}{"_all": "fox"}},
		}}}, want: 2},
		{name: "field kept", query: map[string]interface{}{"match": map[string]interface{}{"tag": "dog"}}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := Query(tt.query, defaultFieldMappings(false), nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, countHits(t, r, q))
		})
	}
}
