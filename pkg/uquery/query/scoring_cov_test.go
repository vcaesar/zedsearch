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
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	segment "github.com/vcaesar/bluge_segment_api"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"
)

// errReader fails the reader calls of range searchers and doc value loads.
type errReader struct {
	search.Reader
}

func (errReader) NumericRange(string, int64, int64, []byte, []byte) (segment.PostingsIterator, error) {
	return nil, errFake
}

func (errReader) DictionaryIterator(string, segment.Automaton, []byte, []byte) (segment.DictionaryIterator, error) {
	return nil, errFake
}

func (errReader) DocumentValueReader([]string) (segment.DocumentValueReader, error) {
	return nil, errFake
}

func newTestBoosting(positive, negative riot.Query) *boostingQuery {
	return &boostingQuery{positive: positive, negative: negative, negativeBoost: 0.5, boost: 1}
}

func TestBoostingQuery_Validate(t *testing.T) {
	tests := []struct {
		name string
		q    *boostingQuery
		want error
	}{
		{"valid", newTestBoosting(riot.NewMatchAllQuery(), riot.NewMatchNoneQuery()), nil},
		{"positive invalid", newTestBoosting(&fakeQuery{validateErr: errFake}, riot.NewMatchNoneQuery()), errFake},
		{"negative invalid", newTestBoosting(riot.NewMatchAllQuery(), &fakeQuery{validateErr: errFake}), errFake},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, tt.q.Validate(), tt.want)
		})
	}
}

func TestBoostingQuery_Advance(t *testing.T) {
	q, err := BoostingQuery(obj{
		"positive":       obj{"match_all": obj{}},
		"negative":       obj{"range": obj{"age": obj{"gte": 10.0}}},
		"negative_boost": 0.5,
	}, testMappings(), nil)
	require.NoError(t, err)

	want, err := searchScores(t, q, false)
	require.NoError(t, err)
	got, err := searchScores(t, &advanceQuery{inner: q}, false)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.Equal(t, want, got)
	assert.InDelta(t, got["1"]*0.5, got["2"], 1e-9)
}

func TestRescoreSearcher_Errors(t *testing.T) {
	matchAll := riot.NewMatchAllQuery()
	errClose := errors.New("close error")

	t.Run("inner searcher error", func(t *testing.T) {
		s, err := newTestBoosting(&fakeQuery{searcherErr: errFake}, matchAll).Searcher(nil, search.SearcherOptions{})
		assert.Nil(t, s)
		assert.ErrorIs(t, err, errFake)
	})

	searcherErrs := []struct {
		name     string
		positive riot.Query
		wantMsg  string
	}{
		{"marker searcher error", matchAll, ""},
		{"marker searcher error and close error", &fakeQuery{inner: matchAll, closeErr: errClose}, "close searcher: close error"},
	}
	for _, tt := range searcherErrs {
		t.Run(tt.name, func(t *testing.T) {
			withFunctionScoreReader(t, func(r search.Reader, options search.SearcherOptions) {
				s, err := newTestBoosting(tt.positive, &fakeQuery{searcherErr: errFake}).Searcher(r, options)
				assert.Nil(t, s)
				assert.ErrorIs(t, err, errFake)
				if tt.wantMsg != "" {
					assert.ErrorContains(t, err, tt.wantMsg)
				}
			})
		})
	}

	markerAdvanceErr := newTestBoosting(matchAll, &fakeQuery{inner: matchAll, advanceErr: errFake})
	nextErrs := []struct {
		name string
		q    riot.Query
	}{
		{"marker advance error", markerAdvanceErr},
		{"inner next error", newTestBoosting(markerAdvanceErr, riot.NewMatchNoneQuery())},
	}
	for _, tt := range nextErrs {
		t.Run(tt.name, func(t *testing.T) {
			withFunctionScoreReader(t, func(r search.Reader, options search.SearcherOptions) {
				s, err := tt.q.Searcher(r, options)
				require.NoError(t, err)
				defer func() { assert.NoError(t, s.Close()) }()

				ctx := search.NewSearchContext(s.DocumentMatchPoolSize(), 0)
				dm, err := s.Next(ctx)
				assert.Nil(t, dm)
				assert.ErrorIs(t, err, errFake)
			})
		})
	}

	t.Run("inner advance error", func(t *testing.T) {
		q := newTestBoosting(&fakeQuery{inner: matchAll, advanceErr: errFake}, riot.NewMatchNoneQuery())
		_, err := searchScores(t, &advanceQuery{inner: q}, false)
		assert.ErrorIs(t, err, errFake)
	})

	t.Run("marker close error", func(t *testing.T) {
		withFunctionScoreReader(t, func(r search.Reader, options search.SearcherOptions) {
			s, err := newTestBoosting(matchAll, &fakeQuery{inner: matchAll, closeErr: errClose}).Searcher(r, options)
			require.NoError(t, err)
			assert.ErrorIs(t, s.Close(), errClose)
		})
	})
}

func TestPinnedQuery_More(t *testing.T) {
	t.Run("boost and _name accepted", func(t *testing.T) {
		q, err := PinnedQuery(obj{"ids": []interface{}{"2"}, "organic": obj{"match_all": obj{}}, "boost": 2.0, "_name": "n"}, testMappings(), nil)
		require.NoError(t, err)
		assert.IsType(t, &pinnedQuery{}, q)
	})

	t.Run("duplicate and empty ids", func(t *testing.T) {
		q := NewPinnedQuery([]string{"1", "", "1", "2"}, riot.NewMatchAllQuery())
		pq, ok := q.(*pinnedQuery)
		require.True(t, ok)
		assert.Len(t, pq.markers, 2)
	})

	tests := []struct {
		name    string
		organic riot.Query
		want    error
	}{
		{"valid organic", riot.NewMatchAllQuery(), nil},
		{"invalid organic", &fakeQuery{validateErr: errFake}, errFake},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, ok := NewPinnedQuery([]string{"1"}, tt.organic).(*pinnedQuery)
			require.True(t, ok)
			assert.ErrorIs(t, pq.Validate(), tt.want)
		})
	}
}

func TestQuery_ScoringParseErrors(t *testing.T) {
	tests := []struct {
		name string
		q    obj
		want string
	}{
		{"pinned", obj{"pinned": obj{"organic": obj{"match_all": obj{}}}}, "[pinned] failed to parse field"},
		{"script_score", obj{"script_score": obj{"script": "_score"}}, "[script_score] failed to parse field"},
		{"rank_feature", obj{"rank_feature": obj{}}, "[rank_feature] failed to parse field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Query(tt.q, testMappings(), nil)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestRankFeatureQuery_More(t *testing.T) {
	t.Run("exists searcher error", func(t *testing.T) {
		q, err := RankFeatureQuery(obj{"field": "age"})
		require.NoError(t, err)
		s, err := q.Searcher(errReader{}, search.SearcherOptions{})
		assert.Nil(t, s)
		assert.ErrorIs(t, err, errFake)
	})

	t.Run("document without value", func(t *testing.T) {
		q, err := RankFeatureQuery(obj{"field": "age"})
		require.NoError(t, err)
		rq, ok := q.(*rankFeatureQuery)
		require.True(t, ok)
		keep, err := rq.score(&search.DocumentMatch{}, &search.DocumentMatch{})
		require.NoError(t, err)
		assert.False(t, keep)
	})

	t.Run("negative score drops documents", func(t *testing.T) {
		scores, err := searchScores(t, mustRankFeature(t, obj{"field": "age", "boost": -1.0}), false)
		require.NoError(t, err)
		assert.Empty(t, scores)
	})
}

func mustRankFeature(t *testing.T, body obj) riot.Query {
	t.Helper()
	q, err := RankFeatureQuery(body)
	require.NoError(t, err)
	return q
}

func TestScriptScoreQuery_MathFunctions(t *testing.T) {
	ages := map[string]float64{"1": 1, "2": 10, "3": 100}
	tests := []struct {
		script string
		fn     func(age float64) float64
	}{
		{"Math.log(doc['age'].value)", math.Log},
		{"Math.log10(doc['age'].value)", math.Log10},
		{"Math.log1p(doc['age'].value)", math.Log1p},
		{"Math.sqrt(doc['age'].value)", math.Sqrt},
		{"Math.exp(doc['age'].value / 100)", func(a float64) float64 { return math.Exp(a / 100) }},
		{"Math.abs(-doc['age'].value)", func(a float64) float64 { return a }},
		{"Math.pow(doc['age'].value, 2)", func(a float64) float64 { return a * a }},
		{"Math.min(doc['age'].value, 10)", func(a float64) float64 { return math.Min(a, 10) }},
		{"Math.max(doc['age'].value, 10)", func(a float64) float64 { return math.Max(a, 10) }},
	}
	for _, tt := range tests {
		t.Run(tt.script, func(t *testing.T) {
			scores, _ := rankScores(t, obj{"script_score": obj{"query": obj{"match_all": obj{}}, "script": tt.script}})
			require.Len(t, scores, len(ages))
			for id, age := range ages {
				assert.InDelta(t, tt.fn(age), scores[id], 1e-9, id)
			}
		})
	}
}

func TestScriptScoreQuery_NonNumberArgument(t *testing.T) {
	assert.True(t, math.IsNaN(num("x")))

	q, err := ScriptScoreQuery(obj{"query": obj{"match_all": obj{}}, "script": "Math.abs('x')"}, testMappings(), nil)
	require.NoError(t, err)
	_, err = searchScores(t, q, false)
	assert.ErrorContains(t, err, "finite non-negative")
}

func TestScriptScoreQuery_Advance(t *testing.T) {
	tests := []struct {
		name    string
		body    obj
		want    map[string]float64
		wantErr string
	}{
		{
			name: "all kept",
			body: obj{"query": obj{"match_all": obj{}}, "script": "doc['age'].value"},
			want: map[string]float64{"1": 1, "2": 10, "3": 100},
		},
		{
			name: "min_score drops documents",
			body: obj{"query": obj{"match_all": obj{}}, "script": "doc['age'].value", "min_score": 10.0},
			want: map[string]float64{"2": 10, "3": 100},
		},
		{
			name:    "invalid score",
			body:    obj{"query": obj{"match_all": obj{}}, "script": "-doc['age'].value"},
			wantErr: "finite non-negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := ScriptScoreQuery(tt.body, testMappings(), nil)
			require.NoError(t, err)
			scores, err := searchScores(t, &advanceQuery{inner: q}, false)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, scores)
		})
	}
}

func TestScriptScoreSearcher_Errors(t *testing.T) {
	t.Run("inner searcher error", func(t *testing.T) {
		q := &scriptScoreQuery{inner: &fakeQuery{searcherErr: errFake}, boost: 1}
		s, err := q.Searcher(nil, search.SearcherOptions{})
		assert.Nil(t, s)
		assert.ErrorIs(t, err, errFake)
	})

	t.Run("doc values load error", func(t *testing.T) {
		q, err := ScriptScoreQuery(obj{"query": obj{"match_all": obj{}}, "script": "doc['age'].value"}, testMappings(), nil)
		require.NoError(t, err)
		withFunctionScoreReader(t, func(r search.Reader, options search.SearcherOptions) {
			s, err := q.Searcher(r, options)
			require.NoError(t, err)
			defer func() { assert.NoError(t, s.Close()) }()
			ss, ok := s.(*scoringSearcher)
			require.True(t, ok)
			ss.reader = errReader{Reader: r}

			ctx := search.NewSearchContext(s.DocumentMatchPoolSize(), 0)
			dm, err := s.Next(ctx)
			assert.Nil(t, dm)
			assert.ErrorIs(t, err, errFake)
		})
	})
}
