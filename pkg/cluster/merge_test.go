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

func TestMergeBucketListMultiTerms(t *testing.T) {
	def := meta.Aggregations{MultiTerms: &meta.AggregationMultiTerms{Size: 1, Order: map[string]string{"_key": "asc"}}}
	out := mergeBucketList([]interface{}{bucket("b", 5)}, []interface{}{bucket("a", 1)}, def)
	require.Len(t, out, 1)
	assert.Equal(t, "a", out[0].(map[string]interface{})["key"])
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
