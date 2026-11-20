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
	"context"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/aggregations"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

// searchScores runs q over the function score index and returns score per
// document id.
func searchScores(t *testing.T, q riot.Query, explain bool) (map[string]float64, error) {
	t.Helper()

	writer := newFunctionScoreIndex(t)
	defer func() { assert.NoError(t, writer.Close()) }()
	reader, err := writer.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, reader.Close()) }()

	req := riot.NewTopNSearch(10, q)
	if explain {
		req.ExplainScores()
	}
	dmi, err := reader.Search(context.Background(), req)
	if err != nil {
		return nil, err
	}
	scores := make(map[string]float64)
	match, err := dmi.Next()
	for err == nil && match != nil {
		if explain {
			assert.NotNil(t, match.Explanation)
		}
		var id string
		require.NoError(t, match.VisitStoredFields(func(field string, value []byte) bool {
			if field == "_id" {
				id = string(value)
				return false
			}
			return true
		}))
		scores[id] = match.Score
		match, err = dmi.Next()
	}
	return scores, err
}

// withFunctionScoreReader runs fn with the search.Reader of the function
// score index.
func withFunctionScoreReader(t *testing.T, fn func(r search.Reader, options search.SearcherOptions)) {
	t.Helper()

	writer := newFunctionScoreIndex(t)
	defer func() { assert.NoError(t, writer.Close()) }()
	reader, err := writer.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, reader.Close()) }()

	withSearchReader(t, reader, fn)
}

func newTestFunctionScore(inner riot.Query, filter riot.Query) *functionScoreQuery {
	return &functionScoreQuery{
		inner:     inner,
		functions: []*scoreFunction{{filter: filter, weight: 2, hasWeight: true}},
		boost:     1,
		maxBoost:  defaultMaxBoost,
		scoreMode: "multiply",
		boostMode: "replace",
	}
}

func TestFunctionScoreQuery_MoreFunctions(t *testing.T) {
	tests := []struct {
		name  string
		query map[string]interface{}
		want  map[string]float64
	}{
		{
			name:  "no query matches all",
			query: map[string]interface{}{"weight": 2.0, "boost_mode": "replace"},
			want:  map[string]float64{"1": 2, "2": 2, "3": 2},
		},
		{
			name:  "log",
			query: map[string]interface{}{"field_value_factor": map[string]interface{}{"field": "age", "modifier": "log"}, "boost_mode": "replace"},
			want:  map[string]float64{"1": 0, "2": 1, "3": 2},
		},
		{
			name:  "log of zero scores zero",
			query: map[string]interface{}{"field_value_factor": map[string]interface{}{"field": "unknown", "modifier": "log", "missing": 0.0}, "boost_mode": "replace"},
			want:  map[string]float64{"1": 0, "2": 0, "3": 0},
		},
		{
			name:  "ln2p",
			query: map[string]interface{}{"field_value_factor": map[string]interface{}{"field": "unknown", "modifier": "ln2p", "missing": 0.0}, "boost_mode": "replace"},
			want:  map[string]float64{"1": math.Ln2, "2": math.Ln2, "3": math.Ln2},
		},
		{
			name:  "sqrt of negative scores zero",
			query: map[string]interface{}{"field_value_factor": map[string]interface{}{"field": "unknown", "modifier": "sqrt", "missing": -4.0}, "boost_mode": "replace"},
			want:  map[string]float64{"1": 0, "2": 0, "3": 0},
		},
		{
			name:  "reciprocal of zero scores zero",
			query: map[string]interface{}{"field_value_factor": map[string]interface{}{"field": "unknown", "modifier": "reciprocal", "missing": 0.0}, "boost_mode": "replace"},
			want:  map[string]float64{"1": 0, "2": 0, "3": 0},
		},
		{
			name:  "decay of missing field keeps full score",
			query: map[string]interface{}{"gauss": map[string]interface{}{"unknown": map[string]interface{}{"origin": 1.0, "scale": 1.0}}, "boost_mode": "replace"},
			want:  map[string]float64{"1": 1, "2": 1, "3": 1},
		},
		{
			name: "decay accepts multi_value_mode",
			query: map[string]interface{}{"linear": map[string]interface{}{
				"age":              map[string]interface{}{"origin": 1.0, "scale": 9.0, "decay": 0.5},
				"multi_value_mode": "min",
			}, "boost_mode": "replace"},
			want: map[string]float64{"1": 1, "2": 0.5, "3": 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scores := searchFunctionScore(t, tt.query)
			require.Len(t, scores, len(tt.want))
			for id, want := range tt.want {
				assert.InDelta(t, want, scores[id], 1e-9, "doc %s", id)
			}
		})
	}

	t.Run("random_score without field uses document number", func(t *testing.T) {
		query := map[string]interface{}{"random_score": map[string]interface{}{"seed": 7.0}, "boost_mode": "replace"}
		first := searchFunctionScore(t, query)
		require.Len(t, first, 3)
		assert.Equal(t, first, searchFunctionScore(t, query))
		for id, score := range first {
			assert.GreaterOrEqual(t, score, 0.0, "doc %s", id)
			assert.Less(t, score, 1.0, "doc %s", id)
		}
	})
}

func TestFunctionScoreQuery_DateDecay(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{name: "days", params: map[string]interface{}{"origin": "2026-01-01T00:00:00Z", "scale": "10d", "offset": "1d"}},
		{name: "go duration", params: map[string]interface{}{"origin": "2026-01-01T00:00:00Z", "scale": " 12h "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := FunctionScoreQuery(map[string]interface{}{
				"gauss": map[string]interface{}{meta.TimeFieldName: tt.params},
			}, testMappings(), nil)
			require.NoError(t, err)
			fsq, ok := q.(*functionScoreQuery)
			require.True(t, ok)
			assert.Equal(t, []string{meta.TimeFieldName}, fsq.fields)
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{in: "10d", want: float64(240 * 3600 * 1e9)},
		{in: "1.5d", want: float64(36 * 3600 * 1e9)},
		{in: "30m", want: float64(30 * 60 * 1e9)},
		{in: "xd", wantErr: true},
		{in: "soon", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			d, err := parseDuration(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, float64(d))
		})
	}
}

func TestFunctionScoreQuery_Advance(t *testing.T) {
	t.Run("min_score drops documents", func(t *testing.T) {
		q, err := FunctionScoreQuery(map[string]interface{}{
			"field_value_factor": map[string]interface{}{"field": "age"},
			"boost_mode":         "replace",
			"min_score":          10.0,
		}, testMappings(), nil)
		require.NoError(t, err)

		scores, err := searchScores(t, &advanceQuery{inner: q}, false)
		require.NoError(t, err)
		assert.Equal(t, map[string]float64{"2": 10, "3": 100}, scores)
	})

	t.Run("invalid score", func(t *testing.T) {
		q, err := FunctionScoreQuery(map[string]interface{}{
			"field_value_factor": map[string]interface{}{"field": "age", "factor": -1.0},
		}, testMappings(), nil)
		require.NoError(t, err)

		_, err = searchScores(t, &advanceQuery{inner: q}, false)
		assert.ErrorContains(t, err, "finite non-negative")
	})
}

func TestFunctionScoreQuery_Explain(t *testing.T) {
	q, err := FunctionScoreQuery(map[string]interface{}{"weight": 3.0, "boost_mode": "replace"}, testMappings(), nil)
	require.NoError(t, err)

	scores, err := searchScores(t, q, true)
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"1": 3, "2": 3, "3": 3}, scores)
}

func TestFunctionScoreSearcher_Errors(t *testing.T) {
	matchAllQuery := riot.NewMatchAllQuery()
	errClose := errors.New("close error")

	t.Run("inner searcher error", func(t *testing.T) {
		s, err := newTestFunctionScore(&fakeQuery{searcherErr: errFake}, nil).Searcher(nil, search.SearcherOptions{})
		assert.Nil(t, s)
		assert.ErrorIs(t, err, errFake)
	})

	t.Run("validate delegates to inner query", func(t *testing.T) {
		assert.ErrorIs(t, newTestFunctionScore(&fakeQuery{validateErr: errFake}, nil).Validate(), errFake)
	})

	tests := []struct {
		name    string
		inner   riot.Query
		filter  riot.Query
		wantErr error
		wantMsg string
	}{
		{
			name:    "filter searcher error",
			inner:   matchAllQuery,
			filter:  &fakeQuery{searcherErr: errFake},
			wantErr: errFake,
		},
		{
			name:    "filter searcher error and close error",
			inner:   &fakeQuery{inner: matchAllQuery, closeErr: errClose},
			filter:  &fakeQuery{searcherErr: errFake},
			wantErr: errFake,
			wantMsg: "close searcher: close error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFunctionScoreReader(t, func(r search.Reader, options search.SearcherOptions) {
				s, err := newTestFunctionScore(tt.inner, tt.filter).Searcher(r, options)
				assert.Nil(t, s)
				assert.ErrorIs(t, err, tt.wantErr)
				if tt.wantMsg != "" {
					assert.ErrorContains(t, err, tt.wantMsg)
				}
			})
		})
	}

	t.Run("filter close error", func(t *testing.T) {
		withFunctionScoreReader(t, func(r search.Reader, options search.SearcherOptions) {
			s, err := newTestFunctionScore(matchAllQuery, &fakeQuery{inner: matchAllQuery, closeErr: errClose}).Searcher(r, options)
			require.NoError(t, err)
			assert.ErrorIs(t, s.Close(), errClose)
		})
	})

	t.Run("filter advance error", func(t *testing.T) {
		withFunctionScoreReader(t, func(r search.Reader, options search.SearcherOptions) {
			s, err := newTestFunctionScore(matchAllQuery, &fakeQuery{inner: matchAllQuery, advanceErr: errFake}).Searcher(r, options)
			require.NoError(t, err)
			defer func() { assert.NoError(t, s.Close()) }()

			ctx := search.NewSearchContext(s.DocumentMatchPoolSize(), 0)
			dm, err := s.Next(ctx)
			assert.Nil(t, dm)
			assert.ErrorIs(t, err, errFake)
		})
	})
}

// TestFunctionScoreQuery_Aggregations pins that loading the function fields
// leaves the collector's doc values intact: other fields stay visible and the
// function fields are not counted twice.
func TestFunctionScoreQuery_Aggregations(t *testing.T) {
	writer := newFunctionScoreIndex(t)
	defer func() { assert.NoError(t, writer.Close()) }()
	reader, err := writer.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, reader.Close()) }()

	q, err := FunctionScoreQuery(map[string]interface{}{
		"query":              matchAll(),
		"field_value_factor": map[string]interface{}{"field": "age", "modifier": "sqrt"},
	}, testMappings(), nil)
	require.NoError(t, err)

	req := riot.NewTopNSearch(10, q)
	req.AddAggregation("tags", aggregations.NewTermsAggregation(search.Field("tag"), 10))
	req.AddAggregation("age", aggregations.Sum(search.Field("age")))
	dmi, err := reader.Search(context.Background(), req)
	require.NoError(t, err)
	for match, err := dmi.Next(); match != nil || err != nil; match, err = dmi.Next() {
		require.NoError(t, err)
	}

	tags := make(map[string]uint64)
	for _, bucket := range dmi.Aggregations().Buckets("tags") {
		tags[bucket.Name()] = bucket.Count()
	}
	assert.Equal(t, map[string]uint64{"fruit": 2, "sky": 1}, tags)
	assert.Equal(t, 111.0, dmi.Aggregations().Metric("age"))
}
