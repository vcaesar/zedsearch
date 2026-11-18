/* Copyright 2026 AtomAI. and Contributors
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
		})
	}
}

func TestHistogramCalculatorMerge(t *testing.T) {
	agg := NewHistogramAggregation(search.Field("age"), 10, 0, nil, nil, 1, 100)
	a := agg.Calculator().(*HistogramCalculator)
	b := agg.Calculator().(*HistogramCalculator)
	for _, d := range numericDocs(t, []float64{1, 12}) {
		a.Consume(d)
	}
	for _, d := range numericDocs(t, []float64{5, 25}) {
		b.Consume(d)
	}
	a.Finish()
	b.Finish()
	a.Merge(b)
	assert.Equal(t, map[string]uint64{"0": 2, "10": 1, "20": 1}, histogramBuckets(a))
}
