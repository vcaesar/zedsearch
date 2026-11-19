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

package aggregation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/aggregations"
)

// bucketKey formats in the local time zone, so expected keys are formatted
// the same way.
func localKey(s string) string {
	return utcDay(s).Local().Format(time.RFC3339)
}

func runAutoDateHistogram(t *testing.T, agg *AutoDateHistogramAggregation, times ...string) *AutoDateHistogramCalculator {
	t.Helper()
	calc := agg.Calculator().(*AutoDateHistogramCalculator)
	ts := make([]time.Time, 0, len(times))
	for _, s := range times {
		ts = append(ts, utcDay(s))
	}
	for _, d := range dateDocs(t, ts...) {
		calc.Consume(d)
	}
	calc.Finish()
	return calc
}

func sumCounts(buckets []*search.Bucket) uint64 {
	var rv uint64
	for _, b := range buckets {
		rv += b.Count()
	}
	return rv
}

func TestAutoDateHistogramIntervals(t *testing.T) {
	tests := []struct {
		minimum string
		want    int
	}{
		{minimum: "second", want: 21},
		{minimum: "minute", want: 17},
		{minimum: "hour", want: 13},
		{minimum: "day", want: 10},
		{minimum: "month", want: 8},
		{minimum: "year", want: 5},
		{minimum: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.minimum, func(t *testing.T) {
			agg := NewAutoDateHistogramAggregation(search.Field("ts"), 10, tt.minimum, time.RFC3339, time.Local)
			assert.Len(t, agg.getIntervals(), tt.want)
		})
	}
}

func TestAutoDateHistogramCalculator(t *testing.T) {
	agg := NewAutoDateHistogramAggregation(search.Field("ts"), 10, "day", time.RFC3339, time.Local)
	calc := runAutoDateHistogram(t, agg, "2026-01-01T00:00:00Z", "2026-01-01T12:00:00Z", "2026-01-03T00:00:00Z")

	assert.Equal(t, map[string]uint64{
		localKey("2026-01-01T00:00:00Z"): 2,
		localKey("2026-01-02T00:00:00Z"): 0,
		localKey("2026-01-03T00:00:00Z"): 1,
	}, bucketCounts(calc.Buckets()))
	assert.Equal(t, "1d", calc.Interval())
	assert.Equal(t, 0, calc.Other())
}

// Too many buckets widen the interval until they fit.
func TestAutoDateHistogramCalculatorWidensInterval(t *testing.T) {
	t.Run("bucket count", func(t *testing.T) {
		// 1970-01-01 and 2026-01-01 are Thursdays, so the 7d buckets align
		agg := NewAutoDateHistogramAggregation(search.Field("ts"), 2, "day", time.RFC3339, time.Local)
		calc := runAutoDateHistogram(t, agg, "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z")
		assert.Equal(t, "7d", calc.Interval())
		assert.Equal(t, map[string]uint64{localKey("2026-01-01T00:00:00Z"): 3}, bucketCounts(calc.Buckets()))
	})
	t.Run("aggregation terms size", func(t *testing.T) {
		agg := NewAutoDateHistogramAggregation(search.Field("ts"), 10, "second", time.RFC3339, time.Local)
		calc := runAutoDateHistogram(t, agg, "2026-01-01T00:00:00Z", "2026-01-01T02:00:00Z")
		assert.Equal(t, "30m", calc.Interval())
		assert.Len(t, calc.Buckets(), 5)
		assert.Equal(t, uint64(2), sumCounts(calc.Buckets()))
	})
	t.Run("month interval", func(t *testing.T) {
		agg := NewAutoDateHistogramAggregation(search.Field("ts"), 3, "month", time.RFC3339, time.Local)
		calc := runAutoDateHistogram(t, agg, "2026-01-10T00:00:00Z", "2026-02-10T00:00:00Z", "2026-03-10T00:00:00Z")
		assert.LessOrEqual(t, len(calc.Buckets()), 3)
		assert.Equal(t, uint64(3), sumCounts(calc.Buckets()))
	})
	t.Run("year interval", func(t *testing.T) {
		agg := NewAutoDateHistogramAggregation(search.Field("ts"), 3, "year", time.RFC3339, time.Local)
		calc := runAutoDateHistogram(t, agg, "2020-06-10T00:00:00Z", "2021-06-10T00:00:00Z", "2022-06-10T00:00:00Z")
		assert.Len(t, calc.Buckets(), 3)
		assert.Equal(t, uint64(3), sumCounts(calc.Buckets()))
	})
}

func TestAutoDateHistogramCalculatorSortDesc(t *testing.T) {
	agg := NewAutoDateHistogramAggregation(search.Field("ts"), 10, "day", time.RFC3339, time.Local)
	agg.desc = true
	calc := runAutoDateHistogram(t, agg, "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z")
	assert.Equal(t, []string{localKey("2026-01-02T00:00:00Z"), localKey("2026-01-01T00:00:00Z")}, bucketNames(calc.Buckets()))
}

func TestAutoDateHistogramCalculatorMerge(t *testing.T) {
	agg := NewAutoDateHistogramAggregation(search.Field("ts"), 10, "day", time.RFC3339, time.Local)
	a := runAutoDateHistogram(t, agg, "2026-01-01T00:00:00Z")
	b := runAutoDateHistogram(t, agg, "2026-01-01T06:00:00Z", "2026-01-03T00:00:00Z")
	a.Merge(b)

	assert.Equal(t, map[string]uint64{
		localKey("2026-01-01T00:00:00Z"): 2,
		localKey("2026-01-02T00:00:00Z"): 0,
		localKey("2026-01-03T00:00:00Z"): 1,
	}, bucketCounts(a.Buckets()))
	assert.Equal(t, 0, a.Other())
}

func TestAutoDateHistogramCalculatorMergeSkipsBadKeys(t *testing.T) {
	agg := NewAutoDateHistogramAggregation(search.Field("ts"), 10, "day", time.RFC3339, time.Local)
	a := runAutoDateHistogram(t, agg, "2026-01-01T00:00:00Z")
	b := agg.Calculator().(*AutoDateHistogramCalculator)
	b.bucketsList = []*search.Bucket{search.NewBucket("not a time", agg.aggregations)}

	a.Merge(b)
	assert.Equal(t, []string{localKey("2026-01-01T00:00:00Z")}, bucketNames(a.Buckets()))

	// other calculator types are ignored
	a.Merge(NewTermsAggregation(search.Field("ts"), TextValueSource, 10).Calculator())
	assert.Len(t, a.Buckets(), 1)
}

func TestAutoDateHistogramAggregationFields(t *testing.T) {
	agg := NewAutoDateHistogramAggregation(search.Field("ts"), 10, "day", time.RFC3339, time.Local)
	agg.AddAggregation("avg_age", aggregations.Avg(search.Field("age")))
	assert.ElementsMatch(t, []string{"ts", "age"}, agg.Fields())
}
