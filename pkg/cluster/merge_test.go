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
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func hit(index, id string, score float64, source map[string]interface{}) meta.Hit {
	return meta.Hit{Index: index, ID: id, Score: score, Source: source}
}

func response(total int, maxScore float64, took int, hits ...meta.Hit) *meta.SearchResponse {
	return &meta.SearchResponse{
		Took: took,
		Hits: meta.Hits{
			Total:    meta.Total{Value: total},
			MaxScore: maxScore,
			Hits:     hits,
		},
	}
}

func hitIDs(hits []meta.Hit) []string {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return ids
}

func TestMergeSearchHits(t *testing.T) {
	tests := []struct {
		name      string
		responses []*meta.SearchResponse
		query     *meta.ZedQuery
		wantIDs   []string
		wantTotal int
		wantTook  int
		wantScore float64
	}{
		{
			name: "score descending across nodes",
			responses: []*meta.SearchResponse{
				response(2, 0.9, 12, hit("a", "a1", 0.9, nil), hit("a", "a2", 0.2, nil)),
				response(1, 0.5, 30, hit("b", "b1", 0.5, nil)),
			},
			query:     &meta.ZedQuery{Size: 10},
			wantIDs:   []string{"a1", "b1", "a2"},
			wantTotal: 3, wantTook: 30, wantScore: 0.9,
		},
		{
			name: "nil responses are skipped",
			responses: []*meta.SearchResponse{
				nil,
				response(1, 0.4, 5, hit("b", "b1", 0.4, nil)),
			},
			query:     &meta.ZedQuery{Size: 10},
			wantIDs:   []string{"b1"},
			wantTotal: 1, wantTook: 5, wantScore: 0.4,
		},
		{
			name: "size cuts the merged list",
			responses: []*meta.SearchResponse{
				response(2, 0.9, 1, hit("a", "a1", 0.9, nil), hit("a", "a2", 0.7, nil)),
				response(1, 0.8, 1, hit("b", "b1", 0.8, nil)),
			},
			query:     &meta.ZedQuery{Size: 2},
			wantIDs:   []string{"a1", "b1"},
			wantTotal: 3, wantTook: 1, wantScore: 0.9,
		},
		{
			name: "from skips the first hits",
			responses: []*meta.SearchResponse{
				response(2, 0.9, 1, hit("a", "a1", 0.9, nil), hit("a", "a2", 0.7, nil)),
				response(1, 0.8, 1, hit("b", "b1", 0.8, nil)),
			},
			query:     &meta.ZedQuery{From: 1, Size: 2},
			wantIDs:   []string{"b1", "a2"},
			wantTotal: 3, wantTook: 1, wantScore: 0.9,
		},
		{
			name: "from past the end returns nothing",
			responses: []*meta.SearchResponse{
				response(1, 0.9, 1, hit("a", "a1", 0.9, nil)),
			},
			query:     &meta.ZedQuery{From: 5, Size: 2},
			wantIDs:   []string{},
			wantTotal: 1, wantTook: 1, wantScore: 0.9,
		},
		{
			name: "sort by field ascending",
			responses: []*meta.SearchResponse{
				response(2, 0, 1,
					hit("a", "a1", 0, map[string]interface{}{"year": float64(2020)}),
					hit("a", "a2", 0, map[string]interface{}{"year": float64(1990)})),
				response(1, 0, 1, hit("b", "b1", 0, map[string]interface{}{"year": float64(2000)})),
			},
			query:     &meta.ZedQuery{Size: 10, Sort: []interface{}{"year"}},
			wantIDs:   []string{"a2", "b1", "a1"},
			wantTotal: 3, wantTook: 1,
		},
		{
			name: "sort by field descending",
			responses: []*meta.SearchResponse{
				response(2, 0, 1,
					hit("a", "a1", 0, map[string]interface{}{"year": float64(2020)}),
					hit("a", "a2", 0, map[string]interface{}{"year": float64(1990)})),
				response(1, 0, 1, hit("b", "b1", 0, map[string]interface{}{"year": float64(2000)})),
			},
			query:     &meta.ZedQuery{Size: 10, Sort: []interface{}{map[string]interface{}{"year": "desc"}}},
			wantIDs:   []string{"a1", "b1", "a2"},
			wantTotal: 3, wantTook: 1,
		},
		{
			name: "missing sort values go last",
			responses: []*meta.SearchResponse{
				response(1, 0, 1, hit("a", "a1", 0, map[string]interface{}{})),
				response(1, 0, 1, hit("b", "b1", 0, map[string]interface{}{"year": float64(2000)})),
			},
			query:     &meta.ZedQuery{Size: 10, Sort: "year"},
			wantIDs:   []string{"b1", "a1"},
			wantTotal: 2, wantTook: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeSearch(tt.responses, tt.query)
			assert.Equal(t, tt.wantIDs, hitIDs(got.Hits.Hits))
			assert.Equal(t, tt.wantTotal, got.Hits.Total.Value)
			assert.Equal(t, tt.wantTook, got.Took)
			assert.InDelta(t, tt.wantScore, got.Hits.MaxScore, 0.0001)
		})
	}
}

func TestMergeSearchShardsAndErrors(t *testing.T) {
	first := response(1, 1, 1, hit("a", "a1", 1, nil))
	first.Shards = meta.Shards{Total: 2, Successful: 2}
	second := response(1, 1, 1, hit("b", "b1", 1, nil))
	second.Shards = meta.Shards{Total: 3, Successful: 2, Failed: 1}
	second.Error = "shard failed"
	second.TimedOut = true

	got := MergeSearch([]*meta.SearchResponse{first, second}, &meta.ZedQuery{Size: 10})
	assert.Equal(t, meta.Shards{Total: 5, Successful: 4, Failed: 1}, got.Shards)
	assert.Equal(t, "shard failed", got.Error)
	assert.True(t, got.TimedOut)
}

func valueAgg(value float64) meta.AggregationResponse {
	return meta.AggregationResponse{Value: value}
}

func avgAgg(value float64, count uint64, sum float64) meta.AggregationResponse {
	return meta.AggregationResponse{
		Value: value,
		Aggregations: map[string]meta.AggregationResponse{
			PartialAggName: {Count: &count, Sum: &sum},
		},
	}
}

func bucketAgg(buckets ...map[string]interface{}) meta.AggregationResponse {
	list := make([]interface{}, 0, len(buckets))
	for _, bucket := range buckets {
		list = append(list, bucket)
	}
	return meta.AggregationResponse{Buckets: list}
}

func bucket(key string, docCount float64) map[string]interface{} {
	return map[string]interface{}{"key": key, "doc_count": docCount}
}

func TestMergeSearchAggregations(t *testing.T) {
	tests := []struct {
		name   string
		left   meta.AggregationResponse
		right  meta.AggregationResponse
		def    meta.Aggregations
		assert func(t *testing.T, got meta.AggregationResponse)
	}{
		{
			name: "sum adds up", left: valueAgg(3), right: valueAgg(4),
			def: meta.Aggregations{Sum: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 7.0, got.Value, 0.0001)
			},
		},
		{
			name: "value_count adds up", left: valueAgg(3), right: valueAgg(4),
			def: meta.Aggregations{ValueCount: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 7.0, got.Value, 0.0001)
			},
		},
		{
			name: "max keeps the largest", left: valueAgg(9), right: valueAgg(4),
			def: meta.Aggregations{Max: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 9.0, got.Value, 0.0001)
			},
		},
		{
			name: "min keeps the smallest", left: valueAgg(9), right: valueAgg(4),
			def: meta.Aggregations{Min: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 4.0, got.Value, 0.0001)
			},
		},
		{
			name:  "avg is weighted by the partial counters",
			left:  avgAgg(10, 3, 30), // 3 docs, sum 30
			right: avgAgg(2, 1, 2),   // 1 doc, sum 2
			def:   meta.Aggregations{Avg: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 8.0, got.Value, 0.0001) // 32 / 4
				assert.NotContains(t, got.Aggregations, PartialAggName)
			},
		},
		{
			name: "avg without counters falls back to the mean",
			left: valueAgg(10), right: valueAgg(2),
			def: meta.Aggregations{Avg: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 6.0, got.Value, 0.0001)
			},
		},
		{
			name:  "buckets union by key",
			left:  bucketAgg(bucket("red", 3), bucket("blue", 2)),
			right: bucketAgg(bucket("red", 1), bucket("green", 5)),
			def:   meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "color"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				buckets, ok := got.Buckets.([]interface{})
				require.True(t, ok)
				require.Len(t, buckets, 3)
				// doc_count descending: green 5, red 4, blue 2
				assert.Equal(t, "green", buckets[0].(map[string]interface{})["key"])
				assert.InDelta(t, 5.0, buckets[0].(map[string]interface{})["doc_count"], 0.0001)
				assert.Equal(t, "red", buckets[1].(map[string]interface{})["key"])
				assert.InDelta(t, 4.0, buckets[1].(map[string]interface{})["doc_count"], 0.0001)
			},
		},
		{
			name: "sub aggregations of a bucket recurse",
			left: bucketAgg(map[string]interface{}{
				"key": "red", "doc_count": float64(3),
				"total": map[string]interface{}{"value": float64(30)},
			}),
			right: bucketAgg(map[string]interface{}{
				"key": "red", "doc_count": float64(1),
				"total": map[string]interface{}{"value": float64(5)},
			}),
			def: meta.Aggregations{
				Terms: &meta.AggregationsTerms{Field: "color"},
				Aggregations: map[string]meta.Aggregations{
					"total": {Sum: &meta.AggregationMetric{Field: "n"}},
				},
			},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				buckets, ok := got.Buckets.([]interface{})
				require.True(t, ok)
				require.Len(t, buckets, 1)
				merged := buckets[0].(map[string]interface{})
				assert.InDelta(t, 4.0, merged["doc_count"], 0.0001)
				total := merged["total"].(map[string]interface{})
				assert.InDelta(t, 35.0, total["value"], 0.0001)
			},
		},
		{
			name: "single bucket sums doc_count",
			left: meta.AggregationResponse{DocCount: uintPtr(4)},
			right: meta.AggregationResponse{
				DocCount: uintPtr(6),
			},
			def: meta.Aggregations{Missing: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				require.NotNil(t, got.DocCount)
				assert.Equal(t, uint64(10), *got.DocCount)
			},
		},
		{
			name: "min ignores a node without values", left: meta.AggregationResponse{}, right: valueAgg(4),
			def: meta.Aggregations{Min: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 4.0, got.Value, 0.0001)
			},
		},
		{
			name: "max ignores a node without values", left: valueAgg(-3), right: meta.AggregationResponse{},
			def: meta.Aggregations{Max: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, -3.0, got.Value, 0.0001)
			},
		},
		{
			name:  "numeric terms rank by doc_count and keep size",
			left:  bucketAgg(bucket("1", 1), bucket("2", 5)),
			right: bucketAgg(bucket("3", 4), bucket("1", 1)),
			def:   meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "n", Size: 2}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				buckets, ok := got.Buckets.([]interface{})
				require.True(t, ok)
				require.Len(t, buckets, 2)
				assert.Equal(t, "2", buckets[0].(map[string]interface{})["key"])
				assert.Equal(t, "3", buckets[1].(map[string]interface{})["key"])
			},
		},
		{
			name:  "range buckets keep request order",
			left:  bucketAgg(bucket("*-10", 1), bucket("10-*", 9)),
			right: bucketAgg(bucket("*-10", 1), bucket("10-*", 2)),
			def:   meta.Aggregations{Range: &meta.AggregationRange{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				buckets, ok := got.Buckets.([]interface{})
				require.True(t, ok)
				require.Len(t, buckets, 2)
				assert.Equal(t, "*-10", buckets[0].(map[string]interface{})["key"])
				assert.InDelta(t, 11.0, buckets[1].(map[string]interface{})["doc_count"], 0.0001)
			},
		},
		{
			name:  "histogram buckets sort by key",
			left:  bucketAgg(bucket("20", 1), bucket("0", 1)),
			right: bucketAgg(bucket("10", 7)),
			def:   meta.Aggregations{Histogram: &meta.AggregationHistogram{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				buckets, ok := got.Buckets.([]interface{})
				require.True(t, ok)
				require.Len(t, buckets, 3)
				assert.Equal(t, "0", buckets[0].(map[string]interface{})["key"])
				assert.Equal(t, "10", buckets[1].(map[string]interface{})["key"])
			},
		},
		{
			name: "unknown aggregation keeps the first node result",
			left: valueAgg(11), right: valueAgg(22),
			def: meta.Aggregations{Cardinality: &meta.AggregationMetric{Field: "n"}},
			assert: func(t *testing.T, got meta.AggregationResponse) {
				assert.InDelta(t, 11.0, got.Value, 0.0001)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &meta.ZedQuery{
				Size:         10,
				Aggregations: map[string]meta.Aggregations{"agg": tt.def},
			}
			left := response(1, 0, 1)
			left.Aggregations = map[string]meta.AggregationResponse{"agg": tt.left}
			right := response(1, 0, 1)
			right.Aggregations = map[string]meta.AggregationResponse{"agg": tt.right}

			got := MergeSearch([]*meta.SearchResponse{left, right}, query)
			require.Contains(t, got.Aggregations, "agg")
			tt.assert(t, got.Aggregations["agg"])
		})
	}
}

func TestMergeSearchKeepsSingleResponseAggregation(t *testing.T) {
	only := response(1, 1, 1, hit("a", "a1", 1, nil))
	only.Aggregations = map[string]meta.AggregationResponse{"avg_n": avgAgg(7, 2, 14)}

	got := MergeSearch([]*meta.SearchResponse{only}, &meta.ZedQuery{
		Size:         10,
		Aggregations: map[string]meta.Aggregations{"avg_n": {Avg: &meta.AggregationMetric{Field: "n"}}},
	})
	assert.InDelta(t, 7.0, got.Aggregations["avg_n"].Value, 0.0001)
	assert.NotContains(t, got.Aggregations["avg_n"].Aggregations, PartialAggName,
		"internal counters must not reach the client")
}

func TestDecodeSearchResponseRestoresPartial(t *testing.T) {
	original := response(2, 1, 4, hit("a", "a1", 1, nil))
	original.Aggregations = map[string]meta.AggregationResponse{"avg_n": avgAgg(7, 2, 14)}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	decoded, err := DecodeSearchResponse(data)
	require.NoError(t, err)
	assert.Equal(t, 2, decoded.Hits.Total.Value)
	require.Contains(t, decoded.Aggregations, "avg_n")

	partial, ok := decoded.Aggregations["avg_n"].Aggregations[PartialAggName]
	require.True(t, ok, "a plain decode drops _partial, DecodeSearchResponse must restore it")
	require.NotNil(t, partial.Count)
	require.NotNil(t, partial.Sum)
	assert.Equal(t, uint64(2), *partial.Count)
	assert.InDelta(t, 14.0, *partial.Sum, 0.0001)
}

func TestDecodeSearchResponseInvalid(t *testing.T) {
	_, err := DecodeSearchResponse([]byte("not json"))
	assert.Error(t, err)
}

func uintPtr(v uint64) *uint64 {
	return &v
}

func TestMergeSearchMetricsIgnoreEmptyNodes(t *testing.T) {
	query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{
		"lo": {Min: &meta.AggregationMetric{Field: "n"}},
		"hi": {Max: &meta.AggregationMetric{Field: "n"}},
		"errors": {
			Filter: map[string]interface{}{},
			Aggregations: map[string]meta.Aggregations{
				"lo": {Min: &meta.AggregationMetric{Field: "n"}},
			},
		},
	}}
	full := response(5, 0, 1)
	full.Aggregations = map[string]meta.AggregationResponse{
		"lo": valueAgg(4), "hi": valueAgg(-3),
		"errors": {DocCount: uintPtr(2), Aggregations: map[string]meta.AggregationResponse{"lo": valueAgg(7)}},
	}
	// an empty node renders min and max as 0
	empty := response(0, 0, 1)
	empty.Aggregations = map[string]meta.AggregationResponse{
		"lo": valueAgg(0), "hi": valueAgg(0),
		"errors": {DocCount: uintPtr(0), Aggregations: map[string]meta.AggregationResponse{"lo": valueAgg(0)}},
	}

	for _, order := range [][]*meta.SearchResponse{{full, empty}, {empty, full}} {
		got := MergeSearch(order, query)
		assert.InDelta(t, 4.0, got.Aggregations["lo"].Value, 0.0001)
		assert.InDelta(t, -3.0, got.Aggregations["hi"].Value, 0.0001)
		assert.InDelta(t, 7.0, got.Aggregations["errors"].Aggregations["lo"].Value, 0.0001)
	}
}

// Averages inside buckets carry no _partial counters, they are weighted by
// the bucket doc_count, whatever the response order.
func TestMergeSearchBucketAvgWeighted(t *testing.T) {
	query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{
		"errors": {
			Filter:       map[string]interface{}{},
			Aggregations: map[string]meta.Aggregations{"avg_n": {Avg: &meta.AggregationMetric{Field: "n"}}},
		},
	}}
	node := func(docs uint64, avg float64) *meta.SearchResponse {
		resp := response(int(docs), 0, 1)
		resp.Aggregations = map[string]meta.AggregationResponse{
			"errors": {DocCount: uintPtr(docs), Aggregations: map[string]meta.AggregationResponse{"avg_n": valueAgg(avg)}},
		}
		return resp
	}
	a, b, c := node(3, 10), node(1, 2), node(0, 0)
	for _, order := range [][]*meta.SearchResponse{{a, b, c}, {a, c, b}, {b, a, c}, {b, c, a}, {c, a, b}, {c, b, a}} {
		got := MergeSearch(order, query)
		assert.InDelta(t, 8.0, got.Aggregations["errors"].Aggregations["avg_n"].Value, 0.0001)
	}
}

func TestMergeSearchTermsOrderAndSize(t *testing.T) {
	merge := func(def meta.Aggregations, left, right meta.AggregationResponse) []string {
		query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{"agg": def}}
		l, r := response(1, 0, 1), response(1, 0, 1)
		l.Aggregations = map[string]meta.AggregationResponse{"agg": left}
		r.Aggregations = map[string]meta.AggregationResponse{"agg": right}
		buckets, ok := MergeSearch([]*meta.SearchResponse{l, r}, query).Aggregations["agg"].Buckets.([]interface{})
		require.True(t, ok)
		keys := make([]string, 0, len(buckets))
		for _, b := range buckets {
			keys = append(keys, b.(map[string]interface{})["key"].(string))
		}
		return keys
	}

	left := bucketAgg(bucket("a", 1), bucket("b", 1))
	right := bucketAgg(bucket("a", 1), bucket("c", 5))
	assert.Equal(t, []string{"a", "b"}, merge(meta.Aggregations{Terms: &meta.AggregationsTerms{
		Field: "f", Size: 2, Order: map[string]string{"_key": "asc"}}}, left, right))
	assert.Equal(t, []string{"b", "a", "c"}, merge(meta.Aggregations{Terms: &meta.AggregationsTerms{
		Field: "f", Size: 3, Order: map[string]string{"_count": "asc"}}}, left, right))

	previous := config.Global.AggregationTermsSize
	t.Cleanup(func() { config.Global.AggregationTermsSize = previous })
	config.Global.AggregationTermsSize = 2
	assert.Equal(t, []string{"c", "a"}, merge(meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "f"}}, left, right))
}

func TestMergeSearchTimestampSortAndSizeZero(t *testing.T) {
	at := func(id string, ts time.Time) meta.Hit { return meta.Hit{ID: id, Timestamp: ts} }
	now := time.Now()
	responses := []*meta.SearchResponse{
		response(1, 0, 1, at("old", now.Add(-time.Hour))),
		response(1, 0, 1, at("new", now)),
	}
	got := MergeSearch(responses, &meta.ZedQuery{Size: 10, Sort: []interface{}{map[string]interface{}{"@timestamp": "desc"}}})
	assert.Equal(t, []string{"new", "old"}, hitIDs(got.Hits.Hits))

	got = MergeSearch(responses, &meta.ZedQuery{From: 1, Size: 0})
	assert.Empty(t, got.Hits.Hits)
	assert.Equal(t, 2, got.Hits.Total.Value)
}

func TestDecodeSearchResponseSubAggNamedLikeField(t *testing.T) {
	decode := func(max float64) *meta.SearchResponse {
		data := []byte(`{"hits":{"total":{"value":2}},"aggregations":{"errors":{"doc_count":2,"max":{"value":` +
			strconv.FormatFloat(max, 'f', -1, 64) + `}}}}`)
		resp, err := DecodeSearchResponse(data)
		require.NoError(t, err)
		return resp
	}
	first := decode(7)
	require.Contains(t, first.Aggregations["errors"].Aggregations, "max")
	assert.InDelta(t, 7.0, first.Aggregations["errors"].Aggregations["max"].Value, 0.0001)

	query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{
		"errors": {
			Filter:       map[string]interface{}{},
			Aggregations: map[string]meta.Aggregations{"max": {Max: &meta.AggregationMetric{Field: "n"}}},
		},
	}}
	got := MergeSearch([]*meta.SearchResponse{first, decode(9)}, query)
	assert.InDelta(t, 9.0, got.Aggregations["errors"].Aggregations["max"].Value, 0.0001)
	assert.Equal(t, uint64(4), *got.Aggregations["errors"].DocCount)
}

func TestDecodeSearchResponseEdgeCases(t *testing.T) {
	resp, err := DecodeSearchResponse([]byte(`{"took":3,"hits":{"total":{"value":1}}}`))
	require.NoError(t, err)
	assert.Equal(t, 3, resp.Took)
	assert.Nil(t, resp.Aggregations)

	resp, err = DecodeSearchResponse([]byte(`{"aggregations":{"bad":1,"ok":{"value":2}}}`))
	require.NoError(t, err)
	require.Len(t, resp.Aggregations, 1, "a non-object aggregation is skipped")
	assert.InDelta(t, 2, resp.Aggregations["ok"].Value, 0)
}

func TestAggGenericConversionFailures(t *testing.T) {
	assert.Empty(t, aggToGeneric(meta.AggregationResponse{Value: make(chan int)}))
	assert.Equal(t, meta.AggregationResponse{}, aggFromGeneric(map[string]interface{}{"value": make(chan int)}))
	assert.Equal(t, meta.AggregationResponse{}, aggFromGeneric(map[string]interface{}{"count": "many"}))
}

func TestMergeSearchWithoutQuery(t *testing.T) {
	left := response(1, 1, 1, hit("a", "a1", 1, nil))
	left.Aggregations = map[string]meta.AggregationResponse{"hist": bucketAgg(bucket("2", 1))}
	right := response(1, 2, 1, hit("b", "b1", 2, nil))
	right.Aggregations = map[string]meta.AggregationResponse{"hist": bucketAgg(bucket("1", 1))}

	got := MergeSearch([]*meta.SearchResponse{left, right}, nil)
	assert.Equal(t, []string{"b1", "a1"}, hitIDs(got.Hits.Hits), "no query keeps every hit")
	buckets := got.Aggregations["hist"].Buckets.([]interface{})
	require.Len(t, buckets, 2)
	assert.Equal(t, "1", buckets[0].(map[string]interface{})["key"], "numeric keys sort ascending")
}

func TestMergeAggByShape(t *testing.T) {
	single := mergeAgg(
		map[string]interface{}{"doc_count": float64(2)},
		map[string]interface{}{"doc_count": float64(3)},
		meta.Aggregations{}, "f", 2, 3)
	assert.InDelta(t, 5, single["doc_count"], 0)

	avg := mergeAgg(
		map[string]interface{}{"value": nil},
		map[string]interface{}{"value": nil},
		meta.Aggregations{Avg: &meta.AggregationMetric{}}, "a", 0, 0)
	assert.InDelta(t, 0, avg["value"], 0, "an average over no documents is 0")

	_, ok := partial(map[string]interface{}{PartialAggName: map[string]interface{}{"count": 1}})
	assert.False(t, ok, "a partial without sum is ignored")
}

func TestMergeSubAggsCopiesMissing(t *testing.T) {
	dst := map[string]interface{}{"doc_count": float64(1)}
	src := map[string]interface{}{"doc_count": float64(1), "inner": map[string]interface{}{"value": float64(4)}}
	mergeSubAggs(dst, src, nil, 1, 1)
	assert.Equal(t, src["inner"], dst["inner"])
}

func TestMergeBucketsForms(t *testing.T) {
	list := []interface{}{bucket("a", 1)}
	assert.Equal(t, list, mergeBuckets(list, "bogus", meta.Aggregations{}), "mismatched forms keep dst")

	keyed := map[string]interface{}{"low": map[string]interface{}{"doc_count": float64(1)}}
	assert.Equal(t, keyed, mergeBuckets(keyed, "bogus", meta.Aggregations{}))

	merged := mergeBuckets(
		map[string]interface{}{"low": map[string]interface{}{"doc_count": float64(1)}},
		map[string]interface{}{
			"low":  map[string]interface{}{"doc_count": float64(2)},
			"high": map[string]interface{}{"doc_count": float64(3)},
			"junk": 1,
		}, meta.Aggregations{}).(map[string]interface{})
	assert.InDelta(t, 3, merged["low"].(map[string]interface{})["doc_count"], 0)
	assert.InDelta(t, 3, merged["high"].(map[string]interface{})["doc_count"], 0)
	assert.NotContains(t, merged, "junk")

	assert.Equal(t, list, mergeBuckets(nil, list, meta.Aggregations{}))
	assert.Equal(t, "kept", mergeBuckets("kept", list, meta.Aggregations{}))
}

func TestMergeBucketListSkipsJunk(t *testing.T) {
	out := mergeBucketList(
		[]interface{}{"junk", bucket("x", 1)},
		[]interface{}{1, bucket("y", 3)},
		meta.Aggregations{})
	require.Len(t, out, 2)
	assert.Equal(t, "y", out[0].(map[string]interface{})["key"], "string keys sort by doc_count")
}

func TestFinishAggMultiTerms(t *testing.T) {
	def := meta.Aggregations{MultiTerms: &meta.AggregationMultiTerms{Size: 1, Order: map[string]string{"_key": "asc"}}}
	agg := map[string]interface{}{
		"buckets": mergeBucketList([]interface{}{bucket("b", 5)}, []interface{}{bucket("a", 1)}, def),
	}
	require.Len(t, agg["buckets"], 2, "nothing is cut while nodes are still merged")
	finishAgg(agg, def)
	out := agg["buckets"].([]interface{})
	require.Len(t, out, 1)
	assert.Equal(t, "a", out[0].(map[string]interface{})["key"])
}

func bucketKeys(t *testing.T, buckets interface{}) []string {
	t.Helper()
	list, ok := buckets.([]interface{})
	require.True(t, ok)
	keys := make([]string, 0, len(list))
	for _, b := range list {
		keys = append(keys, bucketKey(b.(map[string]interface{})))
	}
	return keys
}

// A term behind on the first nodes and ahead overall must win: cutting to
// size between two nodes dropped it before the last node was counted.
func TestMergeSearchTermsCutAfterEveryNode(t *testing.T) {
	query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{
		"by": {Terms: &meta.AggregationsTerms{Field: "f", Size: 1}},
	}}
	node := func(buckets ...map[string]interface{}) *meta.SearchResponse {
		resp := response(1, 0, 1)
		resp.Aggregations = map[string]meta.AggregationResponse{"by": bucketAgg(buckets...)}
		return resp
	}

	got := MergeSearch([]*meta.SearchResponse{
		node(bucket("x", 5), bucket("y", 4)),
		node(bucket("x", 5), bucket("y", 4)),
		node(bucket("y", 10)),
	}, query)
	buckets := got.Aggregations["by"].Buckets.([]interface{})
	require.Len(t, buckets, 1)
	assert.Equal(t, "y", buckets[0].(map[string]interface{})["key"])
	assert.InDelta(t, 18, buckets[0].(map[string]interface{})["doc_count"], 0)

	// a single answer carries the widened size and is cut too
	got = MergeSearch([]*meta.SearchResponse{nil, node(bucket("x", 5), bucket("y", 4))}, query)
	assert.Equal(t, []string{"x"}, bucketKeys(t, got.Aggregations["by"].Buckets))
}

// Terms below another aggregation are cut as well, whatever holds them.
func TestMergeSearchTrimsNestedTerms(t *testing.T) {
	inner := map[string]meta.Aggregations{"by": {Terms: &meta.AggregationsTerms{Field: "f", Size: 1}}}
	terms := func(buckets ...map[string]interface{}) map[string]interface{} {
		list := make([]interface{}, 0, len(buckets))
		for _, b := range buckets {
			list = append(list, b)
		}
		return map[string]interface{}{"buckets": list}
	}
	holder := func(by map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"key": "k", "doc_count": float64(1), "by": by}
	}

	tests := []struct {
		name  string
		def   meta.Aggregations
		node  func(by map[string]interface{}) map[string]interface{}
		inner func(agg map[string]interface{}) interface{}
	}{
		{
			name: "single bucket",
			def:  meta.Aggregations{Filter: map[string]interface{}{}, Aggregations: inner},
			node: func(by map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"doc_count": float64(1), "by": by}
			},
			inner: func(agg map[string]interface{}) interface{} { return agg["by"] },
		},
		{
			name: "bucket list",
			def:  meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "g"}, Aggregations: inner},
			node: func(by map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"buckets": []interface{}{"junk", holder(by)}}
			},
			inner: func(agg map[string]interface{}) interface{} {
				for _, b := range agg["buckets"].([]interface{}) {
					if node, ok := b.(map[string]interface{}); ok {
						return node["by"]
					}
				}
				return nil
			},
		},
		{
			name: "keyed buckets",
			def:  meta.Aggregations{Filters: &meta.AggregationFilters{}, Aggregations: inner},
			node: func(by map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"buckets": map[string]interface{}{"k": holder(by)}}
			},
			inner: func(agg map[string]interface{}) interface{} {
				return agg["buckets"].(map[string]interface{})["k"].(map[string]interface{})["by"]
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := tt.node(terms(bucket("x", 1), bucket("y", 7)))
			finishAgg(agg, tt.def)
			by, ok := tt.inner(agg).(map[string]interface{})
			require.True(t, ok)
			assert.Equal(t, []string{"y"}, bucketKeys(t, by["buckets"]))
		})
	}
}

func TestScatterQuery(t *testing.T) {
	previous := config.Global.AggregationTermsSize
	t.Cleanup(func() { config.Global.AggregationTermsSize = previous })
	config.Global.AggregationTermsSize = 100

	query := &meta.ZedQuery{From: 20, Size: 10, Aggregations: map[string]meta.Aggregations{
		"by": {
			Terms: &meta.AggregationsTerms{Field: "f", Size: 10},
			Aggregations: map[string]meta.Aggregations{
				"pair":  {MultiTerms: &meta.AggregationMultiTerms{Size: 4}},
				"total": {Sum: &meta.AggregationMetric{Field: "n"}},
			},
		},
		"all":    {Terms: &meta.AggregationsTerms{Field: "f"}},
		"paged":  {TopHits: &meta.AggregationTopHits{From: 2, Size: 5}},
		"second": {TopHits: &meta.AggregationTopHits{From: 2}},
		"first":  {TopHits: &meta.AggregationTopHits{Size: 5}},
	}}

	sub := ScatterQuery(query)
	assert.Zero(t, sub.From)
	assert.Equal(t, 30, sub.Size, "every owner returns the whole window")
	assert.Equal(t, 25, sub.Aggregations["by"].Terms.Size)
	assert.Equal(t, 16, sub.Aggregations["by"].Aggregations["pair"].MultiTerms.Size)
	assert.Equal(t, 160, sub.Aggregations["all"].Terms.Size, "the default size is widened too")
	assert.Equal(t, query.Aggregations["by"].Aggregations["total"], sub.Aggregations["by"].Aggregations["total"])

	assert.Equal(t, 20, query.From, "the merge needs the query as asked")
	assert.Equal(t, 10, query.Aggregations["by"].Terms.Size)
	assert.Equal(t, 4, query.Aggregations["by"].Aggregations["pair"].MultiTerms.Size)
	assert.Zero(t, query.Aggregations["all"].Terms.Size)

	assert.Equal(t, meta.AggregationTopHits{Size: 7}, *sub.Aggregations["paged"].TopHits, "every owner returns the whole window")
	assert.Equal(t, meta.AggregationTopHits{Size: 5}, *sub.Aggregations["second"].TopHits, "the default size counts")
	assert.Same(t, query.Aggregations["first"].TopHits, sub.Aggregations["first"].TopHits)
	assert.Equal(t, meta.AggregationTopHits{From: 2, Size: 5}, *query.Aggregations["paged"].TopHits)

	sub = ScatterQuery(&meta.ZedQuery{Size: -1})
	assert.Equal(t, -1, sub.Size)
	assert.Nil(t, sub.Aggregations)
}

func topHitsAgg(total int, hits ...meta.AggregationHit) meta.AggregationResponse {
	best := float64(0)
	for _, h := range hits {
		best = max(best, h.Score)
	}
	return meta.AggregationResponse{Hits: &meta.AggregationHits{Total: meta.Total{Value: total}, MaxScore: best, Hits: hits}}
}

func topHit(id string, score float64, source interface{}) meta.AggregationHit {
	return meta.AggregationHit{ID: id, Score: score, Source: source}
}

func topHitIDs(agg meta.AggregationResponse) []string {
	ids := []string{}
	if agg.Hits != nil {
		for _, h := range agg.Hits.Hits {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

// top_hits pools the hits of every node and windows them once: the first
// node's hits alone are not the best of the cluster.
func TestMergeSearchTopHits(t *testing.T) {
	n := func(value interface{}) map[string]interface{} { return map[string]interface{}{"n": value} }
	merge := func(def *meta.AggregationTopHits, nodes ...meta.AggregationResponse) meta.AggregationResponse {
		query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{"top": {TopHits: def}}}
		responses := make([]*meta.SearchResponse, 0, len(nodes))
		for _, agg := range nodes {
			resp := response(1, 0, 1)
			resp.Aggregations = map[string]meta.AggregationResponse{"top": agg}
			responses = append(responses, resp)
		}
		return MergeSearch(responses, query).Aggregations["top"]
	}
	a := topHitsAgg(5, topHit("a1", 0.9, n(1.0)), topHit("a2", 0.2, n(4.0)), topHit("a3", 0.1, nil))
	b := topHitsAgg(2, topHit("b1", 0.5, n([]interface{}{3.0})), topHit("b2", 0.2, n([]interface{}{})))

	tests := []struct {
		name  string
		def   *meta.AggregationTopHits
		nodes []meta.AggregationResponse
		want  []string
	}{
		{name: "best scores of every node", def: &meta.AggregationTopHits{Size: 2}, nodes: []meta.AggregationResponse{a, b}, want: []string{"a1", "b1"}},
		{name: "whatever the order", def: &meta.AggregationTopHits{Size: 2}, nodes: []meta.AggregationResponse{b, a}, want: []string{"a1", "b1"}},
		{name: "default size, ties by id", def: &meta.AggregationTopHits{}, nodes: []meta.AggregationResponse{b, a}, want: []string{"a1", "b1", "a2"}},
		{name: "from applies to the merged list", def: &meta.AggregationTopHits{From: 1, Size: 2}, nodes: []meta.AggregationResponse{a, b}, want: []string{"b1", "a2"}},
		{name: "from past the end", def: &meta.AggregationTopHits{From: 9, Size: 2}, nodes: []meta.AggregationResponse{a, b}, want: []string{}},
		{name: "score order asked for", def: &meta.AggregationTopHits{Size: 1, Sort: "_score"}, nodes: []meta.AggregationResponse{b, a}, want: []string{"a1"}},
		{name: "sort field ascending, missing last", def: &meta.AggregationTopHits{Size: 5, Sort: "n"}, nodes: []meta.AggregationResponse{a, b}, want: []string{"a1", "b1", "a2", "a3", "b2"}},
		{
			name:  "sort field descending",
			def:   &meta.AggregationTopHits{Size: 2, Sort: []interface{}{map[string]interface{}{"n": "desc"}}},
			nodes: []meta.AggregationResponse{a, b}, want: []string{"a2", "b1"},
		},
		{
			name:  "equal sort values order by id",
			def:   &meta.AggregationTopHits{Size: 2, Sort: "n"},
			nodes: []meta.AggregationResponse{topHitsAgg(1, topHit("z", 0, n(1.0))), topHitsAgg(1, topHit("y", 0, n(1.0)))},
			want:  []string{"y", "z"},
		},
		{name: "a single answer is windowed too", def: &meta.AggregationTopHits{From: 1, Size: 1}, nodes: []meta.AggregationResponse{a}, want: []string{"a2"}},
		{name: "a node without the aggregation body", def: &meta.AggregationTopHits{Size: 1}, nodes: []meta.AggregationResponse{{}, b, {}}, want: []string{"b1"}},
		{name: "no node has hits", def: &meta.AggregationTopHits{Size: 1}, nodes: []meta.AggregationResponse{{}, {}}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, topHitIDs(merge(tt.def, tt.nodes...)))
		})
	}

	got := merge(&meta.AggregationTopHits{Size: 2}, a, b)
	assert.Equal(t, 7, got.Hits.Total.Value)
	assert.InDelta(t, 0.9, got.Hits.MaxScore, 0.0001)
}

// top_hits inside the buckets of another aggregation are merged per bucket.
func TestMergeSearchTopHitsInBuckets(t *testing.T) {
	query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{
		"by": {
			Terms:        &meta.AggregationsTerms{Field: "f"},
			Aggregations: map[string]meta.Aggregations{"top": {TopHits: &meta.AggregationTopHits{Size: 1}}},
		},
	}}
	node := func(id string, score float64) *meta.SearchResponse {
		resp := response(1, 0, 1)
		b := bucket("red", 1)
		b["top"] = map[string]interface{}{"hits": map[string]interface{}{
			"total": map[string]interface{}{"value": float64(1)}, "max_score": score,
			"hits": []interface{}{map[string]interface{}{"_id": id, "_score": score}},
		}}
		resp.Aggregations = map[string]meta.AggregationResponse{"by": bucketAgg(b)}
		return resp
	}

	got := MergeSearch([]*meta.SearchResponse{node("low", 1), node("high", 2)}, query)
	buckets := got.Aggregations["by"].Buckets.([]interface{})
	require.Len(t, buckets, 1)
	hits := buckets[0].(map[string]interface{})["top"].(map[string]interface{})["hits"].(map[string]interface{})
	assert.InDelta(t, 2, hits["total"].(map[string]interface{})["value"], 0)
	list := hits["hits"].([]interface{})
	require.Len(t, list, 1)
	assert.Equal(t, "high", list[0].(map[string]interface{})["_id"])
}

// An owner is asked for the sort field of a top_hits whose _source leaves
// it out, and for nothing more when the field is there anyway.
func TestScatterTopHitsSource(t *testing.T) {
	tests := []struct {
		name string
		def  meta.AggregationTopHits
		want interface{} // _source the owner is asked for, nil: def is sent as is
	}{
		{name: "no _source", def: meta.AggregationTopHits{Sort: "n", Source: false}, want: []interface{}{"n"}},
		{name: "other fields", def: meta.AggregationTopHits{Sort: "-n", Source: []interface{}{"a"}}, want: []interface{}{"a", "n"}},
		{
			name: "sort object",
			def:  meta.AggregationTopHits{Sort: []interface{}{map[string]interface{}{"n": "desc"}}, Source: false},
			want: []interface{}{"n"},
		},
		{name: "field listed", def: meta.AggregationTopHits{Sort: "n", Source: []interface{}{"a", "n"}}},
		{name: "empty list is everything", def: meta.AggregationTopHits{Sort: "n", Source: []interface{}{}}},
		{name: "whole _source", def: meta.AggregationTopHits{Sort: "n", Source: true}},
		{name: "default _source", def: meta.AggregationTopHits{Sort: "n"}},
		{name: "score order", def: meta.AggregationTopHits{Sort: "_score", Source: false}},
		{name: "no sort", def: meta.AggregationTopHits{Source: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asked := tt.def
			sub := ScatterQuery(&meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{"top": {TopHits: &asked}}})
			got := sub.Aggregations["top"].TopHits
			assert.Equal(t, tt.def, asked, "the merge needs the query as asked")
			if tt.want == nil {
				assert.Same(t, &asked, got)
				return
			}
			assert.Equal(t, tt.want, got.Source)
			assert.Equal(t, tt.def.Sort, got.Sort)
		})
	}

	paged := ScatterQuery(&meta.ZedQuery{Aggregations: map[string]meta.Aggregations{
		"top": {TopHits: &meta.AggregationTopHits{From: 2, Size: 5, Sort: "n", Source: false}},
	}}).Aggregations["top"].TopHits
	assert.Equal(t, meta.AggregationTopHits{Size: 7, Sort: "n", Source: []interface{}{"n"}}, *paged)
}

// The owners return the sort field whatever _source the client asked for,
// see TestScatterTopHitsSource: the merge orders by it and gives the
// client the _source it asked for.
func TestMergeSearchTopHitsSortOutsideSource(t *testing.T) {
	merge := func(def *meta.AggregationTopHits, nodes ...meta.AggregationResponse) []meta.AggregationHit {
		query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{"top": {TopHits: def}}}
		responses := make([]*meta.SearchResponse, 0, len(nodes))
		for _, agg := range nodes {
			resp := response(1, 0, 1)
			resp.Aggregations = map[string]meta.AggregationResponse{"top": agg}
			responses = append(responses, resp)
		}
		return MergeSearch(responses, query).Aggregations["top"].Hits.Hits
	}
	doc := func(n float64) map[string]interface{} {
		return map[string]interface{}{"n": n, "city": "x", "note": "y"}
	}
	high, low := topHitsAgg(1, topHit("high", 0, doc(10))), topHitsAgg(1, topHit("low", 0, doc(1)))

	tests := []struct {
		name   string
		source interface{}
		want   interface{}
	}{
		{name: "_source disabled", source: false, want: map[string]interface{}{}},
		{name: "_source fields", source: []interface{}{"city"}, want: map[string]interface{}{"city": "x"}},
		{name: "_source wildcard", source: []interface{}{"no*"}, want: map[string]interface{}{"note": "y"}},
		{name: "sort field listed, the owner filtered already", source: []interface{}{"n"}, want: doc(1)},
		{name: "whole _source", source: true, want: doc(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := merge(&meta.AggregationTopHits{Size: 1, Sort: "n", Source: tt.source}, high, low)
			require.Len(t, got, 1)
			assert.Equal(t, "low", got[0].ID, "the smallest value wins, whatever node answered first")
			assert.Equal(t, tt.want, got[0].Source)
		})
	}

	t.Run("a hit without _source keeps none", func(t *testing.T) {
		got := merge(&meta.AggregationTopHits{Size: 2, Sort: "n", Source: false}, low, topHitsAgg(1, topHit("bare", 0, nil)))
		require.Len(t, got, 2)
		assert.Equal(t, "bare", got[1].ID)
		assert.Nil(t, got[1].Source)
	})
}

func TestFilterSource(t *testing.T) {
	hits := func() []interface{} {
		return []interface{}{
			map[string]interface{}{"_id": "a", "_source": map[string]interface{}{"n": 1.0, "city": "x"}},
			map[string]interface{}{"_id": "b", "_source": make(chan int)},
			"junk",
		}
	}

	list := hits()
	filterSource(list, []interface{}{"city"})
	assert.Equal(t, map[string]interface{}{"city": "x"}, list[0].(map[string]interface{})["_source"])
	assert.NotContains(t, list[1], "_source", "a _source that cannot be filtered is not returned whole")

	list = hits()
	filterSource(list, "bogus")
	assert.Equal(t, hits()[0], list[0], "an owner rejects that _source, nothing reaches the merge")
}

// An owner orders a text field by the bytes of its values, the merge must
// not read them as numbers and pick another document.
func TestMergeSearchTopHitsTextSort(t *testing.T) {
	n := func(value interface{}) map[string]interface{} { return map[string]interface{}{"n": value} }
	merge := func(desc bool, values ...interface{}) []string {
		order := "asc"
		if desc {
			order = "desc"
		}
		def := &meta.AggregationTopHits{Size: 1, Sort: []interface{}{map[string]interface{}{"n": order}}}
		query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{"top": {TopHits: def}}}
		responses := make([]*meta.SearchResponse, 0, len(values))
		for i, value := range values {
			resp := response(1, 0, 1)
			resp.Aggregations = map[string]meta.AggregationResponse{"top": topHitsAgg(1, topHit(strconv.Itoa(i), 0, n(value)))}
			responses = append(responses, resp)
		}
		return topHitIDs(MergeSearch(responses, query).Aggregations["top"])
	}

	assert.Equal(t, []string{"1"}, merge(false, "2", "10"), `"10" is before "2" in byte order`)
	assert.Equal(t, []string{"0"}, merge(true, "2", "10"))
	assert.Equal(t, []string{"1"}, merge(false, 10.0, 2.0), "numbers order by value")
	assert.Equal(t, []string{"0"}, merge(false, "apple", "banana"))
	assert.Equal(t, []string{"0"}, merge(false, "2024-01-01T10:00:00+02:00", "2024-01-01T09:00:00Z"), "dates order by time")
	assert.Equal(t, []string{"1"}, merge(false, "2024-01-01T10:00:00Z", "2024-01-01"), "a date and a text are text")
	assert.Equal(t, []string{"1"}, merge(false, "10", 2.0), "mixed values fall back to numbers")
}

// The owners do not run pipelines: each has its own buckets only, before
// the merge ordered and cut them.
func TestScatterQueryLeavesPipelinesOut(t *testing.T) {
	path := &meta.AggregationPipeline{BucketsPath: "by>total"}
	script := &meta.AggregationBucketScript{BucketsPath: map[string]string{"t": "total"}, Script: "params.t"}
	pipelines := map[string]meta.Aggregations{
		"avg_bucket":      {AvgBucket: path},
		"sum_bucket":      {SumBucket: path},
		"min_bucket":      {MinBucket: path},
		"max_bucket":      {MaxBucket: path},
		"stats_bucket":    {StatsBucket: path},
		"cumulative_sum":  {CumulativeSum: path},
		"derivative":      {Derivative: path},
		"serial_diff":     {SerialDiff: &meta.AggregationSerialDiff{AggregationPipeline: *path}},
		"moving_fn":       {MovingFn: &meta.AggregationMovingFn{AggregationPipeline: *path}},
		"bucket_script":   {BucketScript: script},
		"bucket_selector": {BucketSelector: script},
		"bucket_sort":     {BucketSort: &meta.AggregationBucketSort{}},
	}
	for name, def := range pipelines {
		assert.True(t, isPipeline(def), name)
	}
	assert.False(t, isPipeline(meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "f"}}))

	inner := map[string]meta.Aggregations{"total": {Sum: &meta.AggregationMetric{Field: "n"}}}
	for name, def := range pipelines {
		inner[name] = def
	}
	query := &meta.ZedQuery{Aggregations: map[string]meta.Aggregations{
		"by":   {Terms: &meta.AggregationsTerms{Field: "f"}, Aggregations: inner},
		"sum":  {SumBucket: path},
		"only": {Filter: map[string]interface{}{}, Aggregations: map[string]meta.Aggregations{"sum": {SumBucket: path}}},
	}}

	sub := ScatterQuery(query)
	assert.Len(t, sub.Aggregations, 2)
	assert.NotContains(t, sub.Aggregations, "sum")
	assert.Equal(t, map[string]meta.Aggregations{"total": inner["total"]}, sub.Aggregations["by"].Aggregations)
	assert.Empty(t, sub.Aggregations["only"].Aggregations)
	assert.Len(t, query.Aggregations, 3, "the merge needs the query as asked")
	assert.Len(t, query.Aggregations["by"].Aggregations, len(pipelines)+1)

	assert.True(t, hasPipeline(query.Aggregations))
	assert.True(t, hasPipeline(map[string]meta.Aggregations{"only": query.Aggregations["only"]}), "below another aggregation")
	assert.False(t, hasPipeline(sub.Aggregations))
	assert.False(t, hasPipeline(nil))
}

// A sibling pipeline describes the buckets the client gets: every node is
// counted, and only what is left once terms is cut to size.
func TestMergeSearchSiblingPipeline(t *testing.T) {
	total := func(key string, docs, value float64) map[string]interface{} {
		b := bucket(key, docs)
		b["total"] = map[string]interface{}{"value": value}
		return b
	}
	node := func(buckets ...map[string]interface{}) *meta.SearchResponse {
		resp := response(1, 0, 1)
		resp.Aggregations = map[string]meta.AggregationResponse{"by": bucketAgg(buckets...)}
		return resp
	}
	query := func(size int) *meta.ZedQuery {
		return &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{
			"by": {
				Terms:        &meta.AggregationsTerms{Field: "f", Size: size},
				Aggregations: map[string]meta.Aggregations{"total": {Sum: &meta.AggregationMetric{Field: "n"}}},
			},
			"sum":   {SumBucket: &meta.AggregationPipeline{BucketsPath: "by>total"}},
			"count": {SumBucket: &meta.AggregationPipeline{BucketsPath: "by>_count"}},
			"max":   {MaxBucket: &meta.AggregationPipeline{BucketsPath: "by>total"}},
		}}
	}
	nodes := []*meta.SearchResponse{
		node(total("x", 5, 50), total("y", 4, 40), total("z", 1, 10)),
		node(total("y", 10, 100), total("z", 1, 10)),
	}

	t.Run("only the bucket left by size", func(t *testing.T) {
		got := MergeSearch(nodes, query(1))
		buckets, ok := got.Aggregations["by"].Buckets.([]map[string]interface{})
		require.True(t, ok, "buckets are rendered like a node renders them")
		require.Len(t, buckets, 1)
		assert.Equal(t, "y", buckets[0]["key"])
		assert.InDelta(t, 140, got.Aggregations["sum"].Value, 0.0001)
		assert.InDelta(t, 14, got.Aggregations["count"].Value, 0.0001)
		assert.InDelta(t, 140, got.Aggregations["max"].Value, 0.0001)
	})

	t.Run("every node is counted", func(t *testing.T) {
		got := MergeSearch(nodes, query(10))
		assert.InDelta(t, 210, got.Aggregations["sum"].Value, 0.0001)
		assert.InDelta(t, 21, got.Aggregations["count"].Value, 0.0001)
		assert.InDelta(t, 140, got.Aggregations["max"].Value, 0.0001)
	})

	t.Run("a single answer is cut too", func(t *testing.T) {
		got := MergeSearch([]*meta.SearchResponse{nil, nodes[0]}, query(1))
		assert.InDelta(t, 50, got.Aggregations["sum"].Value, 0.0001)
	})

	t.Run("the response encodes like a node's", func(t *testing.T) {
		data, err := json.Marshal(MergeSearch(nodes, query(1)).Aggregations)
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"by": {"buckets": [{"key": "y", "doc_count": 14, "total": {"value": 140}}]},
			"sum": {"value": 140}, "count": {"value": 14}, "max": {"value": 140}
		}`, string(data))
	})

	t.Run("a pipeline that fails is left out", func(t *testing.T) {
		broken := query(1)
		broken.Aggregations["sum"] = meta.Aggregations{SumBucket: &meta.AggregationPipeline{BucketsPath: "nope>total"}}
		got := MergeSearch(nodes, broken)
		assert.NotContains(t, got.Aggregations, "sum")
		assert.Contains(t, got.Aggregations, "by")
	})

	t.Run("no aggregation answered", func(t *testing.T) {
		got := MergeSearch([]*meta.SearchResponse{response(1, 0, 1)}, query(1))
		assert.Empty(t, got.Aggregations)
	})
}

// Pipelines below another aggregation read its merged buckets, whatever
// holds them.
func TestMergeSearchNestedPipelines(t *testing.T) {
	sales := func(key string, docs, value float64) map[string]interface{} {
		b := bucket(key, docs)
		b["sales"] = map[string]interface{}{"value": value}
		return b
	}
	months := func(buckets ...map[string]interface{}) map[string]interface{} {
		list := make([]interface{}, 0, len(buckets))
		for _, b := range buckets {
			list = append(list, b)
		}
		return map[string]interface{}{"buckets": list}
	}
	inner := map[string]meta.Aggregations{
		"months": {
			Histogram: &meta.AggregationHistogram{Field: "month"},
			Aggregations: map[string]meta.Aggregations{
				"sales":  {Sum: &meta.AggregationMetric{Field: "n"}},
				"cumsum": {CumulativeSum: &meta.AggregationPipeline{BucketsPath: "sales"}},
			},
		},
		"total": {SumBucket: &meta.AggregationPipeline{BucketsPath: "months>sales"}},
	}
	left, right := months(sales("1", 1, 10), sales("2", 1, 20)), months(sales("2", 1, 5), sales("3", 1, 1))
	cumsums := func(t *testing.T, agg meta.AggregationResponse) []float64 {
		t.Helper()
		buckets, ok := agg.Buckets.([]map[string]interface{})
		require.True(t, ok)
		values := make([]float64, 0, len(buckets))
		for _, b := range buckets {
			values = append(values, b["cumsum"].(meta.AggregationResponse).Value.(float64))
		}
		return values
	}

	tests := []struct {
		name  string
		def   meta.Aggregations
		node  func(months map[string]interface{}) meta.AggregationResponse
		inner func(t *testing.T, agg meta.AggregationResponse) map[string]meta.AggregationResponse
	}{
		{
			name: "single bucket",
			def:  meta.Aggregations{Filter: map[string]interface{}{}, Aggregations: inner},
			node: func(months map[string]interface{}) meta.AggregationResponse {
				return aggFromGeneric(map[string]interface{}{"doc_count": float64(2), "months": months})
			},
			inner: func(_ *testing.T, agg meta.AggregationResponse) map[string]meta.AggregationResponse {
				return agg.Aggregations
			},
		},
		{
			name: "bucket list",
			def:  meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "g"}, Aggregations: inner},
			node: func(months map[string]interface{}) meta.AggregationResponse {
				return bucketAgg(map[string]interface{}{"key": "k", "doc_count": float64(2), "months": months})
			},
			inner: func(t *testing.T, agg meta.AggregationResponse) map[string]meta.AggregationResponse {
				buckets, ok := agg.Buckets.([]map[string]interface{})
				require.True(t, ok)
				require.Len(t, buckets, 1)
				return map[string]meta.AggregationResponse{
					"months": buckets[0]["months"].(meta.AggregationResponse),
					"total":  buckets[0]["total"].(meta.AggregationResponse),
				}
			},
		},
		{
			name: "keyed buckets",
			def:  meta.Aggregations{Filters: &meta.AggregationFilters{}, Aggregations: inner},
			node: func(months map[string]interface{}) meta.AggregationResponse {
				return meta.AggregationResponse{Buckets: map[string]interface{}{
					"k": map[string]interface{}{"doc_count": float64(2), "months": months},
				}}
			},
			inner: func(t *testing.T, agg meta.AggregationResponse) map[string]meta.AggregationResponse {
				bucket, ok := agg.Buckets.(map[string]interface{})["k"].(meta.AggregationResponse)
				require.True(t, ok)
				assert.Equal(t, uint64(4), *bucket.DocCount)
				return bucket.Aggregations
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{"agg": tt.def}}
			responses := make([]*meta.SearchResponse, 0, 2)
			for _, months := range []map[string]interface{}{left, right} {
				resp := response(1, 0, 1)
				resp.Aggregations = map[string]meta.AggregationResponse{"agg": tt.node(months)}
				data, err := json.Marshal(resp) // as received from a node
				require.NoError(t, err)
				decoded, err := DecodeSearchResponse(data)
				require.NoError(t, err)
				responses = append(responses, decoded)
			}

			got := tt.inner(t, MergeSearch(responses, query).Aggregations["agg"])
			assert.Equal(t, []float64{10, 35, 36}, cumsums(t, got["months"]), "months 1, 2 and 3 of both nodes")
			assert.InDelta(t, 36, got["total"].Value, 0.0001)
		})
	}

	t.Run("junk in a bucket list is dropped", func(t *testing.T) {
		aggs := map[string]meta.AggregationResponse{"by": {Buckets: []interface{}{"junk", bucket("a", 1)}}}
		nodeForm(aggs, map[string]meta.Aggregations{"by": {Terms: &meta.AggregationsTerms{Field: "f"}}, "absent": {}})
		assert.Equal(t, []map[string]interface{}{bucket("a", 1)}, aggs["by"].Buckets)
		assert.NotContains(t, aggs, "absent")
	})
}

func statsAgg(count uint64, min, max, sum float64) meta.AggregationResponse {
	avg := float64(0)
	if count > 0 {
		avg = sum / float64(count)
	}
	return meta.AggregationResponse{Count: &count, Min: &min, Max: &max, Avg: &avg, Sum: &sum}
}

func extendedStatsAgg(count uint64, min, max, sum, squares float64) meta.AggregationResponse {
	agg := statsAgg(count, min, max, sum)
	zero := float64(0)
	agg.SumOfSquares, agg.Variance, agg.StdDeviation = &squares, &zero, &zero
	agg.StdDeviationBounds = &meta.AggregationStdDeviationBounds{}
	return agg
}

// stats are merged from their counters, so the result is the one a single
// node holding every document would give, whatever the response order.
func TestMergeSearchStats(t *testing.T) {
	merge := func(def meta.Aggregations, nodes ...meta.AggregationResponse) meta.AggregationResponse {
		query := &meta.ZedQuery{Size: 10, Aggregations: map[string]meta.Aggregations{"s": def}}
		responses := make([]*meta.SearchResponse, 0, len(nodes))
		for _, agg := range nodes {
			resp := response(1, 0, 1)
			resp.Aggregations = map[string]meta.AggregationResponse{"s": agg}
			responses = append(responses, resp)
		}
		return MergeSearch(responses, query).Aggregations["s"]
	}
	stats := meta.Aggregations{Stats: &meta.AggregationMetric{Field: "n"}}

	t.Run("stats", func(t *testing.T) {
		a, b, empty := statsAgg(3, 4, 9, 18), statsAgg(1, -3, -3, -3), statsAgg(0, 0, 0, 0)
		for _, order := range [][]meta.AggregationResponse{{a, b, empty}, {empty, a, b}, {b, empty, a}} {
			got := merge(stats, order...)
			assert.Equal(t, uint64(4), *got.Count)
			assert.InDelta(t, -3, *got.Min, 0)
			assert.InDelta(t, 9, *got.Max, 0)
			assert.InDelta(t, 15, *got.Sum, 0)
			assert.InDelta(t, 3.75, *got.Avg, 0.0001)
			assert.Nil(t, got.SumOfSquares)
		}
	})

	t.Run("an empty node does not win min or max", func(t *testing.T) {
		for _, order := range [][]meta.AggregationResponse{
			{statsAgg(2, 4, 9, 13), statsAgg(0, 0, 0, 0)},
			{statsAgg(0, 0, 0, 0), statsAgg(2, 4, 9, 13)},
		} {
			got := merge(stats, order...)
			assert.InDelta(t, 4, *got.Min, 0)
			assert.InDelta(t, 9, *got.Max, 0)
		}
		got := merge(stats, statsAgg(2, -9, -4, -13), statsAgg(0, 0, 0, 0))
		assert.InDelta(t, -4, *got.Max, 0)
	})

	t.Run("no values at all", func(t *testing.T) {
		got := merge(meta.Aggregations{ExtendedStats: &meta.AggregationExtendedStats{Field: "n"}},
			extendedStatsAgg(0, 0, 0, 0, 0), extendedStatsAgg(0, 0, 0, 0, 0))
		assert.Zero(t, *got.Count)
		assert.Zero(t, *got.Avg)
		assert.Zero(t, *got.Variance)
	})

	// values 1, 2, 3 on one node and 4 on the other
	for _, tt := range []struct {
		name  string
		sigma float64
		want  float64 // sigma in effect
	}{{"extended_stats", 0, 2}, {"extended_stats sigma", 3, 3}} {
		t.Run(tt.name, func(t *testing.T) {
			def := meta.Aggregations{ExtendedStats: &meta.AggregationExtendedStats{Field: "n", Sigma: tt.sigma}}
			got := merge(def, extendedStatsAgg(3, 1, 3, 6, 14), extendedStatsAgg(1, 4, 4, 4, 16))
			deviation := math.Sqrt(1.25)
			assert.Equal(t, uint64(4), *got.Count)
			assert.InDelta(t, 2.5, *got.Avg, 0.0001)
			assert.InDelta(t, 30, *got.SumOfSquares, 0.0001)
			assert.InDelta(t, 1.25, *got.Variance, 0.0001)
			assert.InDelta(t, deviation, *got.StdDeviation, 0.0001)
			require.NotNil(t, got.StdDeviationBounds)
			assert.InDelta(t, 2.5+tt.want*deviation, got.StdDeviationBounds.Upper, 0.0001)
			assert.InDelta(t, 2.5-tt.want*deviation, got.StdDeviationBounds.Lower, 0.0001)
		})
	}
}

func TestSortTermsBySubAggregation(t *testing.T) {
	withSub := func(key string, value float64) map[string]interface{} {
		b := bucket(key, 1)
		b["stats"] = map[string]interface{}{"max": value, "value": value}
		return b
	}
	buckets := []interface{}{withSub("a", 1), withSub("b", 3), withSub("c", 3)}

	sortTerms(buckets, map[string]string{"stats.max": "desc"})
	assert.Equal(t, "b", buckets[0].(map[string]interface{})["key"], "ties are broken by key")
	assert.Equal(t, "c", buckets[1].(map[string]interface{})["key"])

	sortTerms(buckets, map[string]string{"stats": "asc"})
	assert.Equal(t, "a", buckets[0].(map[string]interface{})["key"])
}

func TestMergeBucketSubAggs(t *testing.T) {
	dst := map[string]interface{}{"key": "k", "doc_count": float64(1), "flag": true}
	src := map[string]interface{}{
		"key": "k", "doc_count": float64(2), "flag": false,
		"total": map[string]interface{}{"value": float64(1)},
	}
	mergeBucket(dst, src, nil)
	assert.InDelta(t, 3, dst["doc_count"], 0)
	assert.Equal(t, true, dst["flag"], "scalar fields are not merged")
	assert.Equal(t, src["total"], dst["total"])
}

func TestBucketKey(t *testing.T) {
	assert.Equal(t, "1.5", bucketKey(map[string]interface{}{"key": 1.5}))
	assert.Equal(t, "2024", bucketKey(map[string]interface{}{"key_as_string": "2024"}))
	assert.Empty(t, bucketKey(map[string]interface{}{}))
}

func TestSortBuckets(t *testing.T) {
	mixed := []interface{}{bucket("a", 1), "junk"}
	sortBuckets(mixed)
	assert.Equal(t, "junk", mixed[1], "a list with junk is left alone")

	byCount := []interface{}{bucket("b", 1), bucket("a", 1), bucket("c", 2)}
	sortBuckets(byCount)
	keys := []string{}
	for _, b := range byCount {
		keys = append(keys, bucketKey(b.(map[string]interface{})))
	}
	assert.Equal(t, []string{"c", "a", "b"}, keys)
}

func TestSortHitsEdgeCases(t *testing.T) {
	hits := []meta.Hit{
		hit("i", "c", 0, map[string]interface{}{}),
		hit("i", "b", 0, map[string]interface{}{}),
		hit("i", "e", 0, map[string]interface{}{"n": float64(1)}),
		hit("i", "d", 0, map[string]interface{}{"n": float64(1)}),
	}
	sortHits(hits, &meta.ZedQuery{Sort: "n"})
	assert.Equal(t, []string{"d", "e", "b", "c"}, hitIDs(hits), "ties and missing values order by id")
}

func TestSortField(t *testing.T) {
	_, _, ok := sortField(&meta.ZedQuery{Sort: "_score"})
	assert.False(t, ok)
	_, _, ok = sortField(&meta.ZedQuery{Sort: 42})
	assert.False(t, ok)
}

func TestParseSort(t *testing.T) {
	tests := []struct {
		name      string
		value     interface{}
		wantField string
		wantDesc  bool
		wantOK    bool
	}{
		{"minus", "-year", "year", true, true},
		{"plus", "+year", "year", false, true},
		{"empty list", []interface{}{}, "", false, false},
		{"string list", []string{"-year"}, "year", true, true},
		{"empty string list", []string{}, "", false, false},
		{"empty map", map[string]interface{}{}, "", false, false},
		{"order object", map[string]interface{}{"year": map[string]interface{}{"order": "DESC"}}, "year", true, true},
		{"order object without order", map[string]interface{}{"year": map[string]interface{}{}}, "year", false, true},
		{"unknown order type", map[string]interface{}{"year": 1}, "year", false, true},
		{"unknown", 42, "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, desc, ok := parseSort(tt.value)
			assert.Equal(t, tt.wantField, field)
			assert.Equal(t, tt.wantDesc, desc)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestHitValue(t *testing.T) {
	withFields := meta.Hit{Fields: map[string]interface{}{"list": []interface{}{"x", "y"}, "empty": []interface{}{}, "one": "z"}}
	value, ok := hitValue(withFields, "list")
	assert.True(t, ok)
	assert.Equal(t, "x", value)
	_, ok = hitValue(withFields, "empty")
	assert.False(t, ok)
	value, ok = hitValue(withFields, "one")
	assert.True(t, ok)
	assert.Equal(t, "z", value)
	_, ok = hitValue(meta.Hit{Source: "raw"}, "one")
	assert.False(t, ok)
}

func TestToFloat(t *testing.T) {
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name   string
		value  interface{}
		want   float64
		wantOK bool
	}{
		{"float32", float32(1.5), 1.5, true},
		{"int", 2, 2, true},
		{"int64", int64(3), 3, true},
		{"uint64", uint64(4), 4, true},
		{"numeric string", "5.5", 5.5, true},
		{"rfc3339 string", when.Format(time.RFC3339), float64(when.UnixNano()), true},
		{"bad string", "nope", 0, false},
		{"time", when, float64(when.UnixNano()), true},
		{"bool", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat(tt.value)
			assert.Equal(t, tt.wantOK, ok)
			assert.InDelta(t, tt.want, got, 0)
		})
	}
	assert.Zero(t, compareValues(1, 1.0))
}

func TestStringify(t *testing.T) {
	assert.Equal(t, "x", stringify("x"))
	assert.Empty(t, stringify(nil))
	assert.Equal(t, "1.25", stringify(1.25))
	assert.Equal(t, `{"a":1}`, stringify(map[string]int{"a": 1}))
	assert.Empty(t, stringify(make(chan int)))
}
