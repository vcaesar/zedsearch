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

package aggregation

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	segment "github.com/vcaesar/bluge_segment_api"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/aggregations"

	zedaggregation "github.com/vcaesar/zedsearch/pkg/bluge/aggregation"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func requestMappings() *meta.Mappings {
	mappings := filterMappings()
	day := meta.NewProperty("date")
	day.Format = "2006-01-02"
	mappings.SetProperty("day", day)
	return mappings
}

// unsupportedAggregation has a calculator Response cannot render.
type unsupportedAggregation struct{}

type unsupportedCalculator struct{}

func (unsupportedAggregation) Fields() []string { return nil }

func (unsupportedAggregation) Calculator() search.Calculator { return unsupportedCalculator{} }

func (unsupportedCalculator) Consume(*search.DocumentMatch) {}

func (unsupportedCalculator) Finish() {}

func (unsupportedCalculator) Merge(search.Calculator) {}

// storedErrReader fails to read stored fields, as a broken segment would.
type storedErrReader struct{ stubMatchReader }

func (r *storedErrReader) DocumentValueReader(_ []string) (segment.DocumentValueReader, error) {
	return r, nil
}

func (r *storedErrReader) VisitStoredFields(_ uint64, _ segment.StoredFieldVisitor) error {
	return errors.New("stored fields unavailable")
}

var globalSub = map[string]meta.Aggregations{"bad": {Global: &struct{}{}}}

func TestRequestNoop(t *testing.T) {
	req := newTestSearchAggregation()
	require.NoError(t, Request(req, nil, requestMappings()))
	require.NoError(t, Request(req, map[string]meta.Aggregations{"a": {Count: &meta.AggregationMetric{}}}, nil))
	assert.Empty(t, req.aggs)
}

func TestRequestAllAggregations(t *testing.T) {
	avgAge := map[string]meta.Aggregations{"avg_age": {Avg: &meta.AggregationMetric{Field: "age"}}}
	tests := []struct {
		name string
		agg  meta.Aggregations
	}{
		{name: "weighted_avg", agg: meta.Aggregations{WeightedAvg: &meta.AggregationMetric{Field: "age", WeightField: "age"}}},
		{name: "max", agg: meta.Aggregations{Max: &meta.AggregationMetric{Field: "age"}}},
		{name: "count", agg: meta.Aggregations{Count: &meta.AggregationMetric{}}},
		{name: "cardinality", agg: meta.Aggregations{Cardinality: &meta.AggregationMetric{Field: "city"}}},
		{name: "terms numeric", agg: meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "age"}, Aggregations: avgAge}},
		{name: "terms bool", agg: meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "flag", Order: map[string]string{"_count": "desc"}}}},
		{name: "range", agg: meta.Aggregations{Range: &meta.AggregationRange{Field: "age", Ranges: []meta.Range{{From: 0, To: 10}}}}},
		{name: "date_range", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{
			Field:    "ts",
			TimeZone: "+08:00",
			Ranges:   []meta.DateRange{{From: "2022-01-01T00:00:00Z", To: "2022-02-01T00:00:00Z"}, {}},
		}}},
		{name: "date_range with the mapping format", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{
			Field:  "day",
			Ranges: []meta.DateRange{{From: "2022-01-01"}},
		}}},
		{name: "date_range with a request format", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{
			Field:  "ts",
			Format: "2006",
			Ranges: []meta.DateRange{{To: "2022"}},
		}}},
		{name: "histogram", agg: meta.Aggregations{Histogram: &meta.AggregationHistogram{Field: "age", Interval: 10}, Aggregations: avgAge}},
		{name: "date_histogram second", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", CalendarInterval: "second"}}},
		{name: "date_histogram minute", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", CalendarInterval: "1m"}}},
		{name: "date_histogram hour", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", CalendarInterval: "hour"}}},
		{name: "date_histogram day", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", CalendarInterval: "1d"}}},
		{name: "date_histogram week", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", CalendarInterval: "week"}}},
		{name: "date_histogram fixed_interval", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{
			Field: "ts", FixedInterval: "1h", TimeZone: "UTC", Format: "2006-01-02",
		}, Aggregations: avgAge}},
		{name: "date_histogram interval", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", Interval: "30m"}}},
		{name: "auto_date_histogram", agg: meta.Aggregations{AutoDateHistogram: &meta.AggregationAutoDateHistogram{Field: "ts"}}},
		{name: "auto_date_histogram with options", agg: meta.Aggregations{AutoDateHistogram: &meta.AggregationAutoDateHistogram{
			Field: "ts", Buckets: 5, MinimumInterval: "day", TimeZone: "-01:00", Format: "2006",
		}, Aggregations: avgAge}},
		{name: "filters with sub aggregations", agg: meta.Aggregations{Filters: &meta.AggregationFilters{
			Filters: map[string]interface{}{"all": obj{"match_all": obj{}}},
		}, Aggregations: avgAge}},
		{name: "multi_terms", agg: meta.Aggregations{MultiTerms: &meta.AggregationMultiTerms{
			Terms: []meta.AggregationMetric{{Field: "flag"}, {Field: "age"}},
			Order: map[string]string{"_count": "desc"},
		}, Aggregations: avgAge}},
		{name: "nested", agg: meta.Aggregations{Nested: &meta.AggregationNested{Path: "comments"}, Aggregations: avgAge}},
		{name: "reverse_nested", agg: meta.Aggregations{ReverseNested: &meta.AggregationNested{}, Aggregations: avgAge}},
		{name: "top_hits with _source fields", agg: meta.Aggregations{TopHits: &meta.AggregationTopHits{Source: []interface{}{"city"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newTestSearchAggregation()
			require.NoError(t, Request(req, map[string]meta.Aggregations{"my_agg": tt.agg}, requestMappings()))
			assert.Contains(t, req.aggs, "my_agg")
		})
	}
}

func TestRequestAllAggregationErrors(t *testing.T) {
	tests := []struct {
		name string
		agg  meta.Aggregations
		want string
	}{
		{name: "terms unsupported type", agg: meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "ts"}}, want: "[terms] aggregation doesn't support values of type: [ts:[date]]"},
		{name: "terms sub aggregation", agg: meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "city"}, Aggregations: globalSub}, want: "[global]"},
		{name: "range needs ranges", agg: meta.Aggregations{Range: &meta.AggregationRange{Field: "age"}}, want: "[range] aggregation needs ranges"},
		{name: "range numeric only", agg: meta.Aggregations{Range: &meta.AggregationRange{Field: "city", Ranges: []meta.Range{{}}}}, want: "only support type numeric"},
		{name: "date_range needs ranges", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{Field: "ts"}}, want: "[date_range] aggregation needs ranges"},
		{name: "date_range time_zone", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{
			Field: "ts", TimeZone: "Nowhere/Zone", Ranges: []meta.DateRange{{}},
		}}, want: "[date_range] time_zone parse err"},
		{name: "date_range from", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{
			Field: "ts", Ranges: []meta.DateRange{{From: "nope"}},
		}}, want: "range value from parse err"},
		{name: "date_range to", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{
			Field: "ts", Ranges: []meta.DateRange{{To: "nope"}},
		}}, want: "range value to parse err"},
		{name: "date_range datetime only", agg: meta.Aggregations{DateRange: &meta.AggregationDateRange{
			Field: "nope", Ranges: []meta.DateRange{{}},
		}}, want: "only support type datetime"},
		{name: "histogram interval", agg: meta.Aggregations{Histogram: &meta.AggregationHistogram{Field: "age"}}, want: "interval must be a positive decimal"},
		{name: "histogram offset", agg: meta.Aggregations{Histogram: &meta.AggregationHistogram{Field: "age", Interval: 5, Offset: 5}}, want: "offset must be in [0, interval)"},
		{name: "histogram type", agg: meta.Aggregations{Histogram: &meta.AggregationHistogram{Field: "city", Interval: 5}}, want: "[histogram] aggregation doesn't support values of type"},
		{name: "histogram sub aggregation", agg: meta.Aggregations{Histogram: &meta.AggregationHistogram{Field: "age", Interval: 5}, Aggregations: globalSub}, want: "[global]"},
		{name: "date_histogram interval", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts"}}, want: "calendar_interval or fixed_interval must be set"},
		{name: "date_histogram calendar_interval", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", CalendarInterval: "decade"}}, want: "calendar_interval must be Date Calendar"},
		{name: "date_histogram fixed_interval", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "ts", FixedInterval: "abc"}}, want: "fixed_interval must be time duration"},
		{name: "date_histogram time_zone", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{
			Field: "ts", CalendarInterval: "month", TimeZone: "Nowhere/Zone",
		}}, want: "[date_histogram] time_zone parse err"},
		{name: "date_histogram type", agg: meta.Aggregations{DateHistogram: &meta.AggregationDateHistogram{Field: "age", CalendarInterval: "month"}}, want: "[date_histogram] aggregation doesn't support values of type"},
		{name: "date_histogram sub aggregation", agg: meta.Aggregations{
			DateHistogram: &meta.AggregationDateHistogram{Field: "ts", CalendarInterval: "month"}, Aggregations: globalSub,
		}, want: "[global]"},
		{name: "auto_date_histogram minimum_interval", agg: meta.Aggregations{AutoDateHistogram: &meta.AggregationAutoDateHistogram{
			Field: "ts", MinimumInterval: "week",
		}}, want: "minimum_interval must be Date Calendar"},
		{name: "auto_date_histogram time_zone", agg: meta.Aggregations{AutoDateHistogram: &meta.AggregationAutoDateHistogram{
			Field: "ts", TimeZone: "Nowhere/Zone",
		}}, want: "[auto_date_histogram] time_zone parse err"},
		{name: "auto_date_histogram type", agg: meta.Aggregations{AutoDateHistogram: &meta.AggregationAutoDateHistogram{Field: "age"}}, want: "[auto_date_histogram] aggregation doesn't support values of type"},
		{name: "auto_date_histogram sub aggregation", agg: meta.Aggregations{
			AutoDateHistogram: &meta.AggregationAutoDateHistogram{Field: "ts"}, Aggregations: globalSub,
		}, want: "[global]"},
		{name: "filter sub aggregation", agg: meta.Aggregations{Filter: obj{"match_all": obj{}}, Aggregations: globalSub}, want: "[global]"},
		{name: "filters predicate", agg: meta.Aggregations{Filters: &meta.AggregationFilters{
			Filters: map[string]interface{}{"bad": obj{"prefix": obj{}}},
		}}, want: "[prefix] query"},
		{name: "filters sub aggregation", agg: meta.Aggregations{Filters: &meta.AggregationFilters{
			Filters: map[string]interface{}{"all": obj{"match_all": obj{}}},
		}, Aggregations: globalSub}, want: "[global]"},
		{name: "multi_terms order", agg: meta.Aggregations{MultiTerms: &meta.AggregationMultiTerms{
			Terms: []meta.AggregationMetric{{Field: "city"}, {Field: "age"}},
			Order: map[string]string{"_count": "desc", "_key": "asc"},
		}}, want: "order supports a single key"},
		{name: "multi_terms sub aggregation", agg: meta.Aggregations{MultiTerms: &meta.AggregationMultiTerms{
			Terms: []meta.AggregationMetric{{Field: "city"}, {Field: "age"}},
		}, Aggregations: globalSub}, want: "[global]"},
		{name: "nested sub aggregation", agg: meta.Aggregations{Nested: &meta.AggregationNested{Path: "comments"}, Aggregations: globalSub}, want: "[global]"},
		{name: "reverse_nested sub aggregation", agg: meta.Aggregations{ReverseNested: &meta.AggregationNested{}, Aggregations: globalSub}, want: "[global]"},
		{name: "ip_range", agg: meta.Aggregations{IPRange: &meta.AggregationIPRange{Field: "ip"}}, want: "[ip_range] aggregation doesn't support"},
		{name: "top_hits negative size", agg: meta.Aggregations{TopHits: &meta.AggregationTopHits{Size: -1}}, want: "size and from must not be negative"},
		{name: "top_hits _source", agg: meta.Aggregations{TopHits: &meta.AggregationTopHits{Source: 5}}, want: "[_source] value should be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Request(newTestSearchAggregation(), map[string]meta.Aggregations{"my_agg": tt.agg}, requestMappings())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestSingleOrder(t *testing.T) {
	tests := []struct {
		name    string
		order   map[string]string
		key     string
		asc     bool
		wantErr string
	}{
		{name: "asc", order: map[string]string{"_key": "asc"}, key: "_key", asc: true},
		{name: "desc", order: map[string]string{"_count": "desc"}, key: "_count"},
		{name: "empty defaults to count", order: map[string]string{}, key: zedaggregation.OrderByCount},
		{name: "several keys", order: map[string]string{"a": "asc", "b": "asc"}, wantErr: "single key"},
		{name: "bad direction", order: map[string]string{"a": "up"}, wantErr: "must be asc or desc, got [up]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, asc, err := singleOrder("terms", tt.order)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.key, key)
			assert.Equal(t, tt.asc, asc)
		})
	}
}

func TestTopHitsSort(t *testing.T) {
	tests := []struct {
		name    string
		sort    interface{}
		field   string
		desc    bool
		wantErr string
	}{
		{name: "nil sorts by score", sort: nil, desc: true},
		{name: "empty list sorts by score", sort: []interface{}{}, desc: true},
		{name: "_score string", sort: "_score", desc: true},
		{name: "-field", sort: "-age", field: "age", desc: true},
		{name: "+field", sort: "+age", field: "age"},
		{name: "field", sort: []interface{}{"age"}, field: "age"},
		{name: "_score asc", sort: obj{"_score": "asc"}},
		{name: "order object", sort: obj{"age": obj{"order": "desc"}}, field: "age", desc: true},
		{name: "several keys in a list", sort: []interface{}{"a", "b"}, wantErr: "single sort key"},
		{name: "several keys in an object", sort: obj{"a": "asc", "b": "asc"}, wantErr: "single sort key"},
		{name: "bad order", sort: obj{"age": "up"}, wantErr: "sort order must be asc or desc, got [up]"},
		{name: "bad type", sort: 42, wantErr: "sort format error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, desc, err := topHitsSort(tt.sort)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.field, field)
			assert.Equal(t, tt.desc, desc)
		})
	}
}

// bucketOf runs Request, lets add attach extra sub aggregations to the
// registered ones, then feeds docs through a bucket.
func bucketOf(
	t *testing.T,
	aggs map[string]meta.Aggregations,
	add func(req *testSearchAggregation),
	docs []*search.DocumentMatch,
) *search.Bucket {
	t.Helper()
	req := newTestSearchAggregation()
	require.NoError(t, Request(req, aggs, requestMappings()))
	if add != nil {
		add(req)
	}
	bucket := search.NewBucket("test", req.aggs)
	for _, d := range docs {
		bucket.Consume(d)
	}
	bucket.Finish()
	return bucket
}

func TestResponseCoverage(t *testing.T) {
	docs := cityDocs(t, []string{"1", "2", "3"}, []string{"sf", "la", "sf"}, []float64{10, 20, 10})

	t.Run("duration", func(t *testing.T) {
		bucket := search.NewBucket("test", map[string]search.Aggregation{"took": aggregations.Duration()})
		bucket.Finish()
		resp, err := Response(bucket)
		require.NoError(t, err)
		assert.IsType(t, int64(0), resp["took"].Value)
	})

	t.Run("numeric keys with sub aggregations", func(t *testing.T) {
		bucket := bucketOf(t, map[string]meta.Aggregations{"ages": {
			Terms:        &meta.AggregationsTerms{Field: "age", Order: map[string]string{"_key": "asc"}},
			Aggregations: map[string]meta.Aggregations{"cities": {Cardinality: &meta.AggregationMetric{Field: "city"}}},
		}}, nil, docs)
		resp, err := Response(bucket)
		require.NoError(t, err)
		assert.JSONEq(t, `{"buckets":[
			{"key":10,"key_as_string":"10","doc_count":2,"cities":{"value":1}},
			{"key":20,"key_as_string":"20","doc_count":1,"cities":{"value":1}}
		]}`, marshalAgg(t, resp["ages"]))
	})

	t.Run("auto_date_histogram interval", func(t *testing.T) {
		bucket := bucketOf(t, map[string]meta.Aggregations{"auto": {
			AutoDateHistogram: &meta.AggregationAutoDateHistogram{Field: "ts"},
		}}, nil, nil)
		resp, err := Response(bucket)
		require.NoError(t, err)
		assert.NotEmpty(t, resp["auto"].Interval)
	})

	t.Run("unsupported calculator", func(t *testing.T) {
		bucket := search.NewBucket("test", map[string]search.Aggregation{"bad": unsupportedAggregation{}})
		_, err := Response(bucket)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "aggregation doesn't support")
	})

	addBad := func(name string) func(req *testSearchAggregation) {
		return func(req *testSearchAggregation) {
			req.aggs[name].(zedaggregation.SearchAggregation).AddAggregation("bad", unsupportedAggregation{})
		}
	}
	subErrors := []struct {
		name string
		agg  meta.Aggregations
	}{
		{name: "filter", agg: meta.Aggregations{Filter: obj{"match_all": obj{}}}},
		{name: "filters", agg: meta.Aggregations{Filters: &meta.AggregationFilters{
			Filters: map[string]interface{}{"all": obj{"match_all": obj{}}},
		}}},
		{name: "multi_terms", agg: meta.Aggregations{MultiTerms: &meta.AggregationMultiTerms{
			Terms: []meta.AggregationMetric{{Field: "city"}, {Field: "age"}},
		}}},
		{name: "terms", agg: meta.Aggregations{Terms: &meta.AggregationsTerms{Field: "city"}}},
	}
	for _, tt := range subErrors {
		t.Run(tt.name+" unsupported sub aggregation", func(t *testing.T) {
			bucket := bucketOf(t, map[string]meta.Aggregations{"my_agg": tt.agg}, addBad("my_agg"), docs)
			_, err := Response(bucket)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "[bad:")
		})
	}

	t.Run("top_hits stored fields error", func(t *testing.T) {
		d := &search.DocumentMatch{Number: 1, Score: 1}
		d.SetReader(&storedErrReader{})
		bucket := bucketOf(t, map[string]meta.Aggregations{"best": {TopHits: &meta.AggregationTopHits{}}}, nil,
			[]*search.DocumentMatch{d})
		_, err := Response(bucket)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[best] top_hits aggregation err")
	})

	t.Run("percentile_ranks t-digest error", func(t *testing.T) {
		bucket := bucketOf(t, map[string]meta.Aggregations{"ranks": {
			PercentileRanks: &meta.AggregationPercentileRanks{Field: "age", Values: []float64{1}},
		}}, nil, ageDocs(t, math.NaN()))
		_, err := Response(bucket)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[ranks] percentile_ranks aggregation err")
	})
}
