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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/riot/numeric"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/aggregations"
)

const dayLayout = "2006-01-02"

func utcDay(s string) time.Time {
	t, err := time.ParseInLocation(time.RFC3339, s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func millis(s string) float64 {
	return float64(utcDay(s).UnixMilli())
}

// dateDocs turns one time per document into matches on field "ts".
func dateDocs(t *testing.T, times ...time.Time) []*search.DocumentMatch {
	t.Helper()
	docs := make([]*search.DocumentMatch, 0, len(times))
	for _, ts := range times {
		docs = append(docs, newTestMatch(t, []string{"ts"}, map[string][][]byte{
			"ts": {numeric.MustNewPrefixCodedInt64(ts.UnixNano(), 0)},
		}))
	}
	return docs
}

func bucketCounts(buckets []*search.Bucket) map[string]uint64 {
	rv := make(map[string]uint64, len(buckets))
	for _, b := range buckets {
		rv[b.Name()] = b.Count()
	}
	return rv
}

func TestDateHistogramCalculator(t *testing.T) {
	tests := []struct {
		name        string
		calendar    string
		fixed       time.Duration
		format      string
		extended    *HistogramBound
		hard        *HistogramBound
		minDocCount int
		times       []string
		want        map[string]uint64
		wantOther   int
	}{
		{
			name: "calendar month", calendar: "month", minDocCount: 1,
			times: []string{"2026-01-15T10:00:00Z", "2026-01-20T00:00:00Z", "2026-03-10T00:00:00Z"},
			want:  map[string]uint64{"2026-01-01": 2, "2026-03-01": 1},
		},
		{
			name: "calendar month fills the gaps", calendar: "1M",
			times: []string{"2026-01-15T10:00:00Z", "2026-01-20T00:00:00Z", "2026-03-10T00:00:00Z"},
			want:  map[string]uint64{"2026-01-01": 2, "2026-02-01": 0, "2026-03-01": 1},
		},
		{
			// 2026-01-15 is a Thursday, weeks start on Sunday
			name: "calendar week", calendar: "week", minDocCount: 1,
			times: []string{"2026-01-15T00:00:00Z", "2026-01-17T00:00:00Z", "2026-01-20T00:00:00Z"},
			want:  map[string]uint64{"2026-01-11": 2, "2026-01-18": 1},
		},
		{
			name: "calendar week fills the gaps", calendar: "1w",
			times: []string{"2026-01-15T00:00:00Z", "2026-01-29T00:00:00Z"},
			want:  map[string]uint64{"2026-01-11": 1, "2026-01-18": 0, "2026-01-25": 1},
		},
		{
			name: "calendar quarter", calendar: "quarter", minDocCount: 1,
			times: []string{
				"2026-02-01T00:00:00Z", "2026-05-01T00:00:00Z",
				"2026-08-01T00:00:00Z", "2026-11-01T00:00:00Z", "2026-12-01T00:00:00Z",
			},
			want: map[string]uint64{"2026-01-01": 1, "2026-04-01": 1, "2026-07-01": 1, "2026-10-01": 2},
		},
		{
			name: "calendar quarter fills the gaps", calendar: "1q",
			times: []string{"2026-01-01T00:00:00Z", "2026-07-01T00:00:00Z"},
			want:  map[string]uint64{"2026-01-01": 1, "2026-04-01": 0, "2026-07-01": 1},
		},
		{
			name: "calendar year", calendar: "year", minDocCount: 1,
			times: []string{"2024-06-01T00:00:00Z", "2026-02-01T00:00:00Z", "2026-03-01T00:00:00Z"},
			want:  map[string]uint64{"2024-01-01": 1, "2026-01-01": 2},
		},
		{
			name: "calendar year fills the gaps", calendar: "1y",
			times: []string{"2024-01-01T00:00:00Z", "2026-01-01T00:00:00Z"},
			want:  map[string]uint64{"2024-01-01": 1, "2025-01-01": 0, "2026-01-01": 1},
		},
		{
			name: "fixed interval", fixed: 24 * time.Hour, minDocCount: 1,
			times: []string{"2026-01-01T01:00:00Z", "2026-01-01T23:00:00Z", "2026-01-03T00:00:00Z"},
			want:  map[string]uint64{"2026-01-01": 2, "2026-01-03": 1},
		},
		{
			name: "fixed interval fills the gaps", fixed: 24 * time.Hour,
			times: []string{"2026-01-01T01:00:00Z", "2026-01-03T00:00:00Z"},
			want:  map[string]uint64{"2026-01-01": 1, "2026-01-02": 0, "2026-01-03": 1},
		},
		{
			name: "min_doc_count drops small buckets", fixed: 24 * time.Hour, minDocCount: 2,
			times: []string{"2026-01-01T01:00:00Z", "2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z", "2026-01-03T05:00:00Z"},
			want:  map[string]uint64{"2026-01-03": 2}, wantOther: 2,
		},
		{
			name: "epoch_millis format", fixed: 24 * time.Hour, format: "epoch_millis", minDocCount: 1,
			times: []string{"2026-01-01T01:00:00Z"},
			want:  map[string]uint64{strconv.FormatInt(utcDay("2026-01-01T00:00:00Z").UnixMilli(), 10): 1},
		},
		{
			name: "extended_bounds widen the range", fixed: 24 * time.Hour,
			extended: &HistogramBound{Min: millis("2026-01-13T00:00:00Z"), Max: millis("2026-01-16T00:00:00Z")},
			times:    []string{"2026-01-15T12:00:00Z"},
			want:     map[string]uint64{"2026-01-13": 0, "2026-01-14": 0, "2026-01-15": 1},
		},
		{
			// hard_bounds replace the range used to fill gaps
			name: "hard_bounds set the range", fixed: 24 * time.Hour,
			hard:  &HistogramBound{Min: millis("2026-01-14T00:00:00Z"), Max: millis("2026-01-16T00:00:00Z")},
			times: []string{"2026-01-12T00:00:00Z", "2026-01-15T12:00:00Z"},
			want:  map[string]uint64{"2026-01-12": 1, "2026-01-14": 0, "2026-01-15": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := tt.format
			if format == "" {
				format = dayLayout
			}
			agg := NewDateHistogramAggregation(search.Field("ts"), tt.calendar, int64(tt.fixed), format,
				time.UTC, tt.extended, tt.hard, tt.minDocCount, 100)
			calc := agg.Calculator().(*DateHistogramCalculator)
			times := make([]time.Time, 0, len(tt.times))
			for _, s := range tt.times {
				times = append(times, utcDay(s))
			}
			for _, d := range dateDocs(t, times...) {
				calc.Consume(d)
			}
			calc.Finish()
			assert.Equal(t, tt.want, bucketCounts(calc.Buckets()))
			assert.Len(t, calc.Buckets(), len(tt.want), "a bucket name is listed twice")
			assert.Equal(t, tt.wantOther, calc.Other())
		})
	}
}

func TestDateHistogramCalculatorSizeAndOrder(t *testing.T) {
	agg := NewDateHistogramAggregation(search.Field("ts"), "month", 0, dayLayout, time.UTC, nil, nil, 1, 2)
	calc := agg.Calculator().(*DateHistogramCalculator)
	for _, d := range dateDocs(t,
		utcDay("2026-03-01T00:00:00Z"), utcDay("2026-01-01T00:00:00Z"),
		utcDay("2026-02-01T00:00:00Z"), utcDay("2026-03-02T00:00:00Z")) {
		calc.Consume(d)
	}
	calc.Finish()
	assert.Equal(t, []string{"2026-01-01", "2026-02-01"}, bucketNames(calc.Buckets()))
	assert.Equal(t, 2, calc.Other())

	calc.desc = true
	calc.Finish()
	assert.Equal(t, []string{"2026-02-01", "2026-01-01"}, bucketNames(calc.Buckets()))
}

func TestDateHistogramCalculatorMerge(t *testing.T) {
	agg := NewDateHistogramAggregation(search.Field("ts"), "", int64(24*time.Hour), dayLayout, time.UTC, nil, nil, 1, 100)
	a := agg.Calculator().(*DateHistogramCalculator)
	b := agg.Calculator().(*DateHistogramCalculator)
	for _, d := range dateDocs(t, utcDay("2026-01-01T00:00:00Z"), utcDay("2026-01-02T00:00:00Z")) {
		a.Consume(d)
	}
	for _, d := range dateDocs(t, utcDay("2026-01-02T05:00:00Z"), utcDay("2026-01-05T00:00:00Z")) {
		b.Consume(d)
	}
	a.Finish()
	b.Finish()
	a.Merge(b)
	assert.Equal(t, map[string]uint64{"2026-01-01": 1, "2026-01-02": 2, "2026-01-05": 1}, bucketCounts(a.Buckets()))
	assert.Equal(t, 0, a.Other())

	// other calculator types are ignored
	a.Merge(NewTermsAggregation(search.Field("ts"), TextValueSource, 10).Calculator())
	assert.Len(t, a.Buckets(), 3)
}

func TestDateHistogramAggregationFields(t *testing.T) {
	agg := NewDateHistogramAggregation(search.Field("ts"), "month", 0, dayLayout, time.UTC, nil, nil, 1, 10)
	agg.AddAggregation("avg_age", aggregations.Avg(search.Field("age")))
	assert.ElementsMatch(t, []string{"ts", "age"}, agg.Fields())
}
