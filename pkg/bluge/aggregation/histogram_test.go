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

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/riot/search"
)

func histogramBuckets(calc *HistogramCalculator) map[string]uint64 {
	rv := make(map[string]uint64)
	for _, b := range calc.Buckets() {
		rv[b.Name()] = b.Count()
	}
	return rv
}

func TestHistogramCalculator(t *testing.T) {
	tests := []struct {
		name        string
		values      []float64
		interval    float64
		offset      float64
		minDocCount int
		want        map[string]uint64
	}{
		{
			name:     "buckets",
			values:   []float64{1, 9, 10, 15, 99, 100},
			interval: 10, minDocCount: 1,
			want: map[string]uint64{"0": 2, "10": 2, "90": 1, "100": 1},
		},
		{
			name:     "offset and negative values",
			values:   []float64{-7, -2, 3, 4.5},
			interval: 5, offset: 1, minDocCount: 1,
			want: map[string]uint64{"-9": 1, "-4": 1, "1": 2},
		},
		{
			name:     "fractional interval",
			values:   []float64{0.1, 0.3, 0.26},
			interval: 0.25, minDocCount: 1,
			want: map[string]uint64{"0": 1, "0.25": 2},
		},
		{
			name:     "min_doc_count 0 fills the gaps",
			values:   []float64{1, 35},
			interval: 10,
			want:     map[string]uint64{"0": 1, "10": 0, "20": 0, "30": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewHistogramAggregation(search.Field("age"), tt.interval, tt.offset, nil, nil, tt.minDocCount, 100)
			calc := agg.Calculator().(*HistogramCalculator)
			for _, d := range numericDocs(t, tt.values) {
				calc.Consume(d)
			}
			calc.Finish()
			assert.Equal(t, tt.want, histogramBuckets(calc))
			assert.Len(t, calc.Buckets(), len(tt.want), "a bucket name is listed twice")
		})
	}
}

func TestHistogramCalculatorMerge(t *testing.T) {
	tests := []struct {
		name        string
		a, b        []float64
		minDocCount int
		size        int
		want        map[string]uint64
	}{
		{
			name: "counts",
			a:    []float64{1, 12}, b: []float64{5, 25},
			minDocCount: 1, size: 100,
			want: map[string]uint64{"0": 2, "10": 1, "20": 1},
		},
		{
			// the gaps of both are filled once, not again on the merge
			name: "min_doc_count 0 keeps one bucket per gap",
			a:    []float64{1, 35}, b: []float64{1, 35},
			size: 4,
			want: map[string]uint64{"0": 2, "10": 0, "20": 0, "30": 2},
		},
		{
			name: "min_doc_count 0 fills the gap between both",
			a:    []float64{1, 5}, b: []float64{41, 45},
			size: 100,
			want: map[string]uint64{"0": 2, "10": 0, "20": 0, "30": 0, "40": 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewHistogramAggregation(search.Field("age"), 10, 0, nil, nil, tt.minDocCount, tt.size)
			a := agg.Calculator().(*HistogramCalculator)
			b := agg.Calculator().(*HistogramCalculator)
			for _, d := range numericDocs(t, tt.a) {
				a.Consume(d)
			}
			for _, d := range numericDocs(t, tt.b) {
				b.Consume(d)
			}
			a.Finish()
			b.Finish()
			a.Merge(b)
			assert.Equal(t, tt.want, histogramBuckets(a))
			assert.Len(t, a.Buckets(), len(tt.want), "a bucket name is listed twice")
		})
	}
}
