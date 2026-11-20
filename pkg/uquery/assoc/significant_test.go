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

package assoc

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery/aggregation"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func intPtr(n int) *int { return &n }

func boolPtr(b bool) *bool { return &b }

// bucketScores maps the bucket keys of a rewritten response to their
// [doc_count, bg_count, score], in order.
func bucketScores(t *testing.T, r meta.AggregationResponse) ([]string, map[string][3]float64) {
	t.Helper()
	buckets, ok := r.Buckets.([]map[string]interface{})
	require.True(t, ok, "%T", r.Buckets)
	keys := make([]string, 0, len(buckets))
	rv := make(map[string][3]float64, len(buckets))
	for _, b := range buckets {
		key := b["key"].(string)
		keys = append(keys, key)
		rv[key] = [3]float64{float64(b["doc_count"].(uint64)), float64(b["bg_count"].(uint64)), b["score"].(float64)}
	}
	return keys, rv
}

func TestSignificantTerms_Search(t *testing.T) {
	r := newTestReader(t, testDocs)
	readers := []*riot.Reader{r}
	run := func(t *testing.T, q riot.Query, aggs map[string]meta.Aggregations) map[string]meta.AggregationResponse {
		t.Helper()
		req := riot.NewTopNSearch(0, q).WithStandardAggregations()
		require.NoError(t, aggregation.Request(req, aggs, testMappings()))
		dmi, err := r.Search(context.Background(), req)
		require.NoError(t, err)
		resp, err := aggregation.Response(dmi.Aggregations())
		require.NoError(t, err)
		require.NoError(t, SignificantTerms(context.Background(), readers, resp, aggs, dmi.Aggregations().Count(), testMappings(), nil))
		return resp
	}

	t.Run("jlh over a keyword field", func(t *testing.T) {
		aggs := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag", MinDocCount: intPtr(1)}}}
		resp := run(t, riot.NewTermQuery("quick").SetField("body"), aggs)
		sig := resp["sig"]
		assert.Equal(t, uint64(4), *sig.DocCount)
		assert.Equal(t, uint64(10), *sig.BgCount)
		keys, scores := bucketScores(t, sig)
		// tech is less frequent in the foreground than in the background
		assert.Equal(t, []string{"animal", "food"}, keys)
		assert.InDelta(t, 0.125, scores["animal"][2], 1e-12)
		assert.Equal(t, [2]float64{2, 4}, [2]float64{scores["animal"][0], scores["animal"][1]})
		assert.InDelta(t, 0.0625, scores["food"][2], 1e-12)

		buf, err := json.Marshal(sig)
		require.NoError(t, err)
		assert.Contains(t, string(buf), `"bg_count":10`)
		assert.Contains(t, string(buf), `"doc_count":4`)
	})

	t.Run("default min_doc_count drops rare terms", func(t *testing.T) {
		aggs := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}}}
		resp := run(t, riot.NewTermQuery("quick").SetField("body"), aggs)
		keys, _ := bucketScores(t, resp["sig"])
		assert.Empty(t, keys)
	})

	t.Run("numeric field counts the background by search", func(t *testing.T) {
		aggs := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "n", MinDocCount: intPtr(1), Percentage: &struct{}{}}}}
		resp := run(t, riot.NewTermQuery("lazy").SetField("body"), aggs)
		buckets := resp["sig"].Buckets.([]map[string]interface{})
		require.Len(t, buckets, 2)
		for _, b := range buckets {
			assert.Equal(t, uint64(1), b["bg_count"])
			assert.Equal(t, 1.0, b["score"])
		}
		assert.Equal(t, "1", buckets[0]["key_as_string"])
		assert.Equal(t, "3", buckets[1]["key_as_string"])
	})

	t.Run("nested under terms buckets", func(t *testing.T) {
		aggs := map[string]meta.Aggregations{"by_tag": {
			Terms:        &meta.AggregationsTerms{Field: "tag"},
			Aggregations: map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "n", MinDocCount: intPtr(1), Percentage: &struct{}{}}}},
		}}
		resp := run(t, riot.NewMatchAllQuery(), aggs)
		buckets, ok := resp["by_tag"].Buckets.([]map[string]interface{})
		require.True(t, ok, "%T", resp["by_tag"].Buckets)
		require.NotEmpty(t, buckets)
		for _, b := range buckets {
			sig := b["sig"].(meta.AggregationResponse)
			assert.Equal(t, b["doc_count"], *sig.DocCount)
			assert.Equal(t, uint64(10), *sig.BgCount)
		}
	})
}

// textResponse is a terms response over body for the foreground "animal"
// documents 1, 2, 3 and 9.
func textResponse() meta.AggregationResponse {
	return meta.AggregationResponse{Buckets: []map[string]interface{}{
		{"key": "dog", "doc_count": uint64(3)},
		{"key": "the", "doc_count": uint64(3)},
		{"key": "lazy", "doc_count": uint64(2)},
		{"key": "fox", "doc_count": uint64(2)},
		{"key": "quick", "doc_count": uint64(2), "sub": meta.AggregationResponse{Value: 1.0}},
	}}
}

func TestSignificantTerms_Rewrite(t *testing.T) {
	readers := testReaders(t)
	tests := []struct {
		name     string
		sig      meta.AggregationSignificantTerms
		wantKeys []string
		wantBg   uint64
	}{
		{"jlh order", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1)}, []string{"dog", "lazy", "the", "fox", "quick"}, 10},
		{"explicit jlh and size", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), Size: 3, JLH: &struct{}{}}, []string{"dog", "lazy", "the"}, 10},
		{"min_doc_count", meta.AggregationSignificantTerms{Field: "body"}, []string{"dog", "the"}, 10},
		{"include regexp", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), Include: "d.*|l.*"}, []string{"dog", "lazy"}, 10},
		{"include list", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), Include: []interface{}{"fox", "the"}}, []string{"the", "fox"}, 10},
		{"exclude", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), Exclude: "the|quick"}, []string{"dog", "lazy", "fox"}, 10},
		{"percentage", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), Percentage: &struct{}{}}, []string{"dog", "lazy", "the", "fox", "quick"}, 10},
		{"chi_square", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), ChiSquare: &meta.SignificanceHeuristic{}}, []string{"dog", "lazy", "the", "fox", "quick"}, 10},
		{"mutual_information", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), MutualInformation: &meta.SignificanceHeuristic{}}, []string{"dog", "lazy", "the", "fox", "quick"}, 10},
		{"gnd", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), GND: &meta.SignificanceHeuristic{}}, []string{"dog", "the", "lazy", "fox", "quick"}, 10},
		{"background_filter", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1),
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "tech"}}}, []string{"dog", "the", "fox", "lazy", "quick"}, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig := tt.sig
			resp := map[string]meta.AggregationResponse{"sig": textResponse()}
			req := map[string]meta.Aggregations{"sig": {SignificantText: &sig}}
			require.NoError(t, SignificantTerms(context.Background(), readers, resp, req, 4, testMappings(), nil))
			keys, _ := bucketScores(t, resp["sig"])
			assert.Equal(t, tt.wantKeys, keys)
			assert.Equal(t, uint64(4), *resp["sig"].DocCount)
			assert.Equal(t, tt.wantBg, *resp["sig"].BgCount)
		})
	}

	t.Run("scores and sub aggregations", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"sig": textResponse()}
		req := map[string]meta.Aggregations{"sig": {SignificantText: &meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1)}}}
		require.NoError(t, SignificantTerms(context.Background(), readers, resp, req, 4, testMappings(), nil))
		_, scores := bucketScores(t, resp["sig"])
		assert.Equal(t, [2]float64{3, 3}, [2]float64{scores["dog"][0], scores["dog"][1]})
		assert.InDelta(t, 1.125, scores["dog"][2], 1e-12)
		assert.InDelta(t, 0.75, scores["lazy"][2], 1e-12)
		assert.InDelta(t, 0.65625, scores["the"][2], 1e-12)
		assert.InDelta(t, 0.2/0.3*0.5, scores["fox"][2], 1e-12)
		buckets := resp["sig"].Buckets.([]map[string]interface{})
		assert.Equal(t, meta.AggregationResponse{Value: 1.0}, buckets[4]["sub"])
	})

	t.Run("background filter counts", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"sig": textResponse()}
		req := map[string]meta.Aggregations{"sig": {SignificantText: &meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1),
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "animal"}}}}}
		require.NoError(t, SignificantTerms(context.Background(), readers, resp, req, 4, testMappings(), nil))
		_, scores := bucketScores(t, resp["sig"])
		// the foreground is the background: nothing is significant
		assert.Empty(t, scores)
		assert.Equal(t, uint64(4), *resp["sig"].BgCount)
	})

	t.Run("nested walks", func(t *testing.T) {
		inner := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1)}}}
		docCount := uint64(4)
		resp := map[string]meta.AggregationResponse{
			"terms":   {Buckets: []map[string]interface{}{{"key": "animal", "doc_count": uint64(4), "sig": textResponse()}, {"key": "none", "doc_count": uint64(0)}}},
			"filter":  {DocCount: &docCount, Aggregations: map[string]meta.AggregationResponse{"sig": textResponse()}},
			"filters": {Buckets: map[string]interface{}{"a": meta.AggregationResponse{DocCount: &docCount, Aggregations: map[string]meta.AggregationResponse{"sig": textResponse()}}, "b": meta.AggregationResponse{DocCount: &docCount}}},
			"plain":   {Value: 1.0},
		}
		req := map[string]meta.Aggregations{
			"terms":   {Terms: &meta.AggregationsTerms{Field: "tag"}, Aggregations: inner},
			"filter":  {Filter: map[string]interface{}{}, Aggregations: inner},
			"filters": {Filters: &meta.AggregationFilters{}, Aggregations: inner},
			"plain":   {Max: &meta.AggregationMetric{Field: "n"}},
			"missing": {Max: &meta.AggregationMetric{Field: "n"}},
		}
		require.NoError(t, SignificantTerms(context.Background(), readers, resp, req, 10, testMappings(), nil))

		sig := resp["terms"].Buckets.([]map[string]interface{})[0]["sig"].(meta.AggregationResponse)
		keys, _ := bucketScores(t, sig)
		assert.Equal(t, []string{"dog", "lazy", "the", "fox", "quick"}, keys)
		keys, _ = bucketScores(t, resp["filter"].Aggregations["sig"])
		assert.Equal(t, []string{"dog", "lazy", "the", "fox", "quick"}, keys)
		filtered := resp["filters"].Buckets.(map[string]interface{})["a"].(meta.AggregationResponse)
		keys, _ = bucketScores(t, filtered.Aggregations["sig"])
		assert.Equal(t, []string{"dog", "lazy", "the", "fox", "quick"}, keys)
	})

	t.Run("no significance aggregation reads no reader", func(t *testing.T) {
		// a nil reader panics on Count
		nilReaders := []*riot.Reader{nil}
		docCount := uint64(4)
		resp := map[string]meta.AggregationResponse{
			"plain":  {Value: 1.0},
			"terms":  {Buckets: []map[string]interface{}{{"key": "a", "doc_count": uint64(4), "max": meta.AggregationResponse{Value: 2.0}}}},
			"filter": {DocCount: &docCount, Aggregations: map[string]meta.AggregationResponse{"max": {Value: 3.0}}},
		}
		sub := map[string]meta.Aggregations{"max": {Max: &meta.AggregationMetric{Field: "n"}}}
		req := map[string]meta.Aggregations{
			"plain":  {Max: &meta.AggregationMetric{Field: "n"}},
			"terms":  {Terms: &meta.AggregationsTerms{Field: "tag"}, Aggregations: sub},
			"filter": {Filter: map[string]interface{}{}, Aggregations: sub},
		}
		assert.NotPanics(t, func() {
			assert.NoError(t, SignificantTerms(context.Background(), nilReaders, resp, req, 10, testMappings(), nil))
		})
		assert.Equal(t, meta.AggregationResponse{Value: 1.0}, resp["plain"])
	})

	t.Run("background_filter alone reads no total", func(t *testing.T) {
		calls := 0
		s := &significance{ctx: context.Background(), readers: readers, mappings: testMappings(), total: func() (uint64, error) {
			calls++
			return totalDocs(readers)
		}}
		resp := map[string]meta.AggregationResponse{"sig": textResponse()}
		req := map[string]meta.Aggregations{"sig": {SignificantText: &meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1),
			BackgroundFilter: map[string]interface{}{"match_all": map[string]interface{}{}}}}}
		require.NoError(t, s.walk(resp, req, 4))
		assert.Equal(t, 0, calls)
	})

	t.Run("background total is loaded once", func(t *testing.T) {
		calls := 0
		s := &significance{ctx: context.Background(), readers: readers, mappings: testMappings(), total: func() (uint64, error) {
			calls++
			return totalDocs(readers)
		}}
		inner := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1)}}}
		resp := map[string]meta.AggregationResponse{"terms": {Buckets: []map[string]interface{}{
			{"key": "a", "doc_count": uint64(4), "sig": textResponse()},
			{"key": "b", "doc_count": uint64(4), "sig": textResponse()},
		}}}
		req := map[string]meta.Aggregations{"terms": {Terms: &meta.AggregationsTerms{Field: "tag"}, Aggregations: inner}}
		require.NoError(t, s.walk(resp, req, 10))
		assert.Equal(t, 1, calls)
		for _, b := range resp["terms"].Buckets.([]map[string]interface{}) {
			assert.Equal(t, uint64(10), *b["sig"].(meta.AggregationResponse).BgCount)
		}
	})

	t.Run("background total error", func(t *testing.T) {
		s := &significance{ctx: context.Background(), readers: readers, mappings: testMappings(), total: func() (uint64, error) {
			return 0, assert.AnError
		}}
		resp := map[string]meta.AggregationResponse{"sig": textResponse()}
		req := map[string]meta.Aggregations{"sig": {SignificantText: &meta.AggregationSignificantTerms{Field: "body"}}}
		assert.ErrorIs(t, s.walk(resp, req, 4), assert.AnError)
	})

	supersetTests := []struct {
		name    string
		sig     meta.AggregationSignificantTerms
		fgSize  uint64
		wantErr string
	}{
		{"chi_square term count above background", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), ChiSquare: &meta.SignificanceHeuristic{},
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "tech"}}}, 4, "background_is_superset: false"},
		{"mutual_information term count above background", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), MutualInformation: &meta.SignificanceHeuristic{},
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "tech"}}}, 4, "background_is_superset: false"},
		{"gnd foreground larger than background", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), GND: &meta.SignificanceHeuristic{},
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "food"}}}, 4, "background_is_superset: false"},
		{"chi_square foreground larger than the index", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), ChiSquare: &meta.SignificanceHeuristic{}}, 11, "background_is_superset: false"},
		{"not superset accepts it", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), ChiSquare: &meta.SignificanceHeuristic{BackgroundIsSuperset: boolPtr(false)},
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "tech"}}}, 4, ""},
		{"gnd not superset accepts it", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), GND: &meta.SignificanceHeuristic{BackgroundIsSuperset: boolPtr(false)},
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "food"}}}, 4, ""},
		{"jlh has no superset check", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1),
			BackgroundFilter: map[string]interface{}{"term": map[string]interface{}{"tag": "tech"}}}, 4, ""},
		{"superset counts accepted", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), ChiSquare: &meta.SignificanceHeuristic{}}, 4, ""},
		{"chi_square more foreground docs without the term", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1), ChiSquare: &meta.SignificanceHeuristic{IncludeNegatives: true}}, 10, "subset docs without ["},
		{"not superset accepts complementary counts", meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1),
			ChiSquare: &meta.SignificanceHeuristic{IncludeNegatives: true, BackgroundIsSuperset: boolPtr(false)}}, 10, ""},
	}
	for _, tt := range supersetTests {
		t.Run(tt.name, func(t *testing.T) {
			sig := tt.sig
			resp := map[string]meta.AggregationResponse{"sig": textResponse()}
			req := map[string]meta.Aggregations{"sig": {SignificantText: &sig}}
			err := SignificantTerms(context.Background(), readers, resp, req, tt.fgSize, testMappings(), nil)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("empty inputs", func(t *testing.T) {
		assert.NoError(t, SignificantTerms(context.Background(), readers, nil, nil, 0, nil, nil))
		resp := map[string]meta.AggregationResponse{"sig": {}}
		req := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "body"}}}
		require.NoError(t, SignificantTerms(context.Background(), readers, resp, req, 0, nil, nil))
		assert.Equal(t, []map[string]interface{}{}, resp["sig"].Buckets)
	})

	errTests := []struct {
		name string
		sig  meta.AggregationSignificantTerms
	}{
		{"two heuristics", meta.AggregationSignificantTerms{Field: "body", JLH: &struct{}{}, GND: &meta.SignificanceHeuristic{}}},
		{"bad include regexp", meta.AggregationSignificantTerms{Field: "body", Include: "["}},
		{"partition include", meta.AggregationSignificantTerms{Field: "body", Include: map[string]interface{}{"partition": 0}}},
		{"bad exclude", meta.AggregationSignificantTerms{Field: "body", Exclude: 1}},
		{"bad background_filter", meta.AggregationSignificantTerms{Field: "body", BackgroundFilter: map[string]interface{}{"nope": map[string]interface{}{}}}},
	}
	for _, tt := range errTests {
		t.Run(tt.name, func(t *testing.T) {
			sig := tt.sig
			resp := map[string]meta.AggregationResponse{"sig": textResponse()}
			req := map[string]meta.Aggregations{"sig": {SignificantTerms: &sig}}
			assert.Error(t, SignificantTerms(context.Background(), readers, resp, req, 4, testMappings(), nil))
		})
	}

	t.Run("nested error", func(t *testing.T) {
		inner := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "body", Include: "["}}}
		docCount := uint64(4)
		for name, r := range map[string]meta.AggregationResponse{
			"terms":   {Buckets: []map[string]interface{}{{"key": "a", "doc_count": uint64(4), "sig": textResponse()}}},
			"filter":  {DocCount: &docCount, Aggregations: map[string]meta.AggregationResponse{"sig": textResponse()}},
			"filters": {Buckets: map[string]interface{}{"a": meta.AggregationResponse{DocCount: &docCount, Aggregations: map[string]meta.AggregationResponse{"sig": textResponse()}}}},
		} {
			resp := map[string]meta.AggregationResponse{name: r}
			req := map[string]meta.Aggregations{name: {Aggregations: inner}}
			assert.Error(t, SignificantTerms(context.Background(), readers, resp, req, 4, testMappings(), nil), name)
		}
	})
}

func TestHeuristics(t *testing.T) {
	t.Run("jlh", func(t *testing.T) {
		assert.InDelta(t, 0.125, jlhScore(2, 4, 4, 10), 1e-12)
		assert.InDelta(t, 0.375, jlhScore(1, 4, 0, 10), 1e-12)
		assert.Equal(t, 0.0, jlhScore(1, 4, 4, 10))
		assert.Equal(t, 0.0, jlhScore(1, 0, 4, 10))
		assert.Equal(t, 0.0, jlhScore(1, 4, 4, 0))
	})
	t.Run("percentage", func(t *testing.T) {
		assert.Equal(t, 0.5, percentageScore(2, 4, 4, 10))
		assert.Equal(t, 0.0, percentageScore(2, 4, 0, 10))
	})
	t.Run("chi_square", func(t *testing.T) {
		assert.InDelta(t, 10.0*16/576, chiSquareScore(2, 4, 4, 10, false, true), 1e-12)
		assert.InDelta(t, 14.0*16/1920, chiSquareScore(2, 4, 4, 10, false, false), 1e-12)
		assert.True(t, math.IsInf(chiSquareScore(1, 4, 4, 10, false, true), -1))
		assert.False(t, math.IsInf(chiSquareScore(1, 4, 4, 10, true, true), 0))
	})
	t.Run("mutual_information", func(t *testing.T) {
		assert.InDelta(t, 0.019974, mutualInformationScore(2, 4, 4, 10, false, true), 1e-5)
		assert.True(t, math.IsInf(mutualInformationScore(1, 4, 4, 10, false, true), -1))
		assert.False(t, math.IsInf(mutualInformationScore(1, 4, 4, 10, true, true), 0))
		assert.False(t, math.IsInf(mutualInformationScore(2, 4, 4, 10, false, false), 0))
		assert.Equal(t, 0.0, miTerm(0, 1, 1, 10))
	})
	t.Run("gnd", func(t *testing.T) {
		assert.InDelta(t, math.Exp(-math.Log(2)/math.Log(2.5)), gndScore(2, 4, 4, 10, true), 1e-12)
		assert.Equal(t, 1.0, gndScore(4, 4, 4, 10, true))
		assert.Equal(t, 0.0, gndScore(0, 4, 4, 10, true))
		assert.Greater(t, gndScore(2, 4, 0, 10, true), 0.0)
		assert.Greater(t, gndScore(2, 4, 4, 10, false), 0.0)
	})
	t.Run("heuristic options", func(t *testing.T) {
		h := &meta.SignificanceHeuristic{BackgroundIsSuperset: boolPtr(false)}
		assert.False(t, superset(h))
		assert.True(t, superset(&meta.SignificanceHeuristic{}))
		score, isSuperset, err := sigHeuristic("significant_terms", &meta.AggregationSignificantTerms{ChiSquare: h})
		require.NoError(t, err)
		assert.False(t, isSuperset)
		assert.InDelta(t, 14.0*16/1920, score(2, 4, 4, 10), 1e-12)
		_, isSuperset, err = sigHeuristic("significant_terms", &meta.AggregationSignificantTerms{GND: &meta.SignificanceHeuristic{}})
		require.NoError(t, err)
		assert.True(t, isSuperset)
		_, isSuperset, err = sigHeuristic("significant_terms", &meta.AggregationSignificantTerms{})
		require.NoError(t, err)
		assert.False(t, isSuperset)
	})
}
