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

package query

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

// analyzerMappings maps "title" with an index analyzer and "body" with a
// search analyzer.
func analyzerMappings() *meta.Mappings {
	m := meta.NewMappings()
	title := meta.NewProperty("text")
	title.Analyzer = "standard"
	m.SetProperty("title", title)
	body := meta.NewProperty("text")
	body.SearchAnalyzer = "simple"
	m.SetProperty("body", body)
	return m
}

// TestQuery_FieldAnalyzer checks every query picks the search analyzer of the
// field, or its index analyzer when there is no search analyzer.
func TestQuery_FieldAnalyzer(t *testing.T) {
	mappings := analyzerMappings()
	for _, field := range []string{"title", "body"} {
		tests := []struct {
			name  string
			parse func() (riot.Query, error)
		}{
			{name: "match", parse: func() (riot.Query, error) {
				return MatchQuery(map[string]interface{}{field: "quick fox"}, mappings, nil)
			}},
			{name: "match_phrase", parse: func() (riot.Query, error) {
				return MatchPhraseQuery(map[string]interface{}{field: "quick fox"}, mappings, nil)
			}},
			{name: "match_phrase_prefix", parse: func() (riot.Query, error) {
				return MatchPhrasePrefixQuery(map[string]interface{}{field: "quick fo"}, mappings, nil)
			}},
			{name: "match_bool_prefix", parse: func() (riot.Query, error) {
				return MatchBoolPrefixQuery(map[string]interface{}{field: "quick fo"}, mappings, nil)
			}},
			{name: "fuzzy", parse: func() (riot.Query, error) {
				return FuzzyQuery(map[string]interface{}{field: "quack"}, mappings, nil)
			}},
			{name: "multi_match", parse: func() (riot.Query, error) {
				return MultiMatchQuery(map[string]interface{}{"query": "quick fox", "fields": []interface{}{field}}, mappings, nil)
			}},
			{name: "query_string", parse: func() (riot.Query, error) {
				return QueryStringQuery(map[string]interface{}{"query": "quick fox", "fields": []interface{}{field}}, mappings, nil)
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name+" "+field, func(t *testing.T) {
				q, err := tt.parse()
				require.NoError(t, err)
				assert.NotNil(t, q)
				if mq, ok := q.(*riot.MatchQuery); ok {
					assert.NotNil(t, mq.Analyzer())
				}
			})
		}
	}
}

func TestQuery_KeptWithoutDefaultFields(t *testing.T) {
	t.Run("nil mappings", func(t *testing.T) {
		q, err := Query(matchAll(), nil, nil)
		require.NoError(t, err)
		assert.IsType(t, &riot.MatchAllQuery{}, q)
	})

	t.Run("_all enabled", func(t *testing.T) {
		q, err := Query(map[string]interface{}{"match": map[string]interface{}{"_all": "fox"}}, defaultFieldMappings(true), nil)
		require.NoError(t, err)
		mq, ok := q.(*riot.MatchQuery)
		require.True(t, ok, "query on _all is not expanded")
		assert.Equal(t, "_all", mq.Field())
	})
}

func TestQuery_KnnParseError(t *testing.T) {
	q, err := Query(map[string]interface{}{"knn": map[string]interface{}{}}, testMappings(), nil)
	assert.Nil(t, q)
	assert.ErrorContains(t, err, "[knn] failed to parse field")
}

func TestRangeQueryTime_IgnoresUnknownField(t *testing.T) {
	q, err := RangeQuery(map[string]interface{}{
		meta.TimeFieldName: map[string]interface{}{"gte": "2026-01-01T00:00:00Z", "unknown": 1.0},
	}, testMappings())
	require.NoError(t, err)
	assert.NotNil(t, q)
}

func TestQueryStringDefaultFields_MoreQueries(t *testing.T) {
	r := defaultFieldReader(t)
	tests := []struct {
		name     string
		query    string
		mappings *meta.Mappings
		want     uint64
	}{
		{name: "fuzzy", query: "quack~1", mappings: defaultFieldMappings(false), want: 1},
		{name: "regexp", query: "/qu.*/", mappings: defaultFieldMappings(false), want: 1},
		{name: "phrase with field analyzer", query: `"brown fox"`, mappings: func() *meta.Mappings {
			m := defaultFieldMappings(false)
			title := meta.NewProperty("text")
			title.Analyzer = "standard"
			m.SetProperty("title", title)
			return m
		}(), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := QueryStringQuery(map[string]interface{}{"query": tt.query}, tt.mappings, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, countHits(t, r, q))
		})
	}
}

func TestWithDefaultFields_FieldlessQueries(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		query     riot.Query
		wantField string
	}{
		{name: "date range", query: riot.NewDateRangeQuery(start, start.Add(time.Hour)), wantField: "@timestamp"},
		{name: "fuzzy", query: riot.NewFuzzyQuery("quack"), wantField: "tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := withDefaultFields(tt.query, lazyDefaultFields(defaultFieldMappings(false), nil, nil, nil))
			bq, ok := q.(*riot.BooleanQuery)
			require.True(t, ok)
			require.NotEmpty(t, bq.Shoulds())
			fq, ok := bq.Shoulds()[0].(interface{ Field() string })
			require.True(t, ok)
			assert.Equal(t, tt.wantField, fq.Field())
		})
	}
}
