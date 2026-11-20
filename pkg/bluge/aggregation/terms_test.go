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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/aggregations"
)

func TestTermsAggregationSetOrder(t *testing.T) {
	// city -> ages; "sf" has the most documents, "la" the highest average age
	cities := []string{"sf", "sf", "sf", "la", "la", "nyc"}
	ages := []float64{10, 20, 30, 90, 80, 50}

	tests := []struct {
		name     string
		order    string
		asc      bool
		useOrder bool
		want     []string
	}{
		{name: "default is count desc", want: []string{"sf", "la", "nyc"}},
		{name: "count asc", order: OrderByCount, asc: true, useOrder: true, want: []string{"nyc", "la", "sf"}},
		{name: "key asc", order: OrderByKey, asc: true, useOrder: true, want: []string{"la", "nyc", "sf"}},
		{name: "key desc", order: OrderByKey, useOrder: true, want: []string{"sf", "nyc", "la"}},
		{name: "sub aggregation desc", order: "avg_age", useOrder: true, want: []string{"la", "nyc", "sf"}},
		{name: "sub aggregation asc", order: "avg_age", asc: true, useOrder: true, want: []string{"sf", "nyc", "la"}},
		{name: "stats metric desc", order: "age_stats.max", useOrder: true, want: []string{"la", "nyc", "sf"}},
		{name: "unknown key keeps insertion order", order: "nope", useOrder: true, want: []string{"sf", "la", "nyc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewTermsAggregation(search.Field("city"), TextValueSource, 10)
			agg.AddAggregation("avg_age", aggregations.Avg(search.Field("age")))
			agg.AddAggregation("age_stats", NewStatsAggregation(search.Field("age")))
			if tt.useOrder {
				agg.SetOrder(tt.order, tt.asc)
			}

			calc := agg.Calculator().(*TermsCalculator)
			for _, d := range cityAgeDocs(t, cities, ages) {
				calc.Consume(d)
			}
			calc.Finish()
			assert.Equal(t, tt.want, bucketNames(calc.Buckets()))
		})
	}
}

// Keyword keys order lexically, numeric field keys numerically.
func TestTermsAggregationKeyOrderByFieldType(t *testing.T) {
	docs := func(t *testing.T) []*search.DocumentMatch {
		rv := []*search.DocumentMatch{}
		for _, v := range []float64{9, 10} {
			rv = append(rv, newTestMatch(t, []string{"code", "age"}, map[string][][]byte{
				"code": textTerms(strconv.FormatFloat(v, 'f', -1, 64)),
				"age":  numTerms(v),
			}))
		}
		return rv
	}
	tests := []struct {
		name    string
		field   string
		srcType int
		want    []string
	}{
		{name: "keyword", field: "code", srcType: TextValueSource, want: []string{"10", "9"}},
		{name: "numeric", field: "age", srcType: NumericValueSource, want: []string{"9", "10"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewTermsAggregation(search.Field(tt.field), tt.srcType, 10)
			agg.SetOrder(OrderByKey, true)
			calc := agg.Calculator().(*TermsCalculator)
			for _, d := range docs(t) {
				calc.Consume(d)
			}
			calc.Finish()
			assert.Equal(t, tt.want, bucketNames(calc.Buckets()))
		})
	}
}

// The FieldSource fast path buckets the first value, as FieldSource.Value
// does, for keyword and numeric-encoded terms and for missing values.
func TestTermsAggregationTextFirstValue(t *testing.T) {
	docs := []*search.DocumentMatch{
		newTestMatch(t, []string{"f"}, map[string][][]byte{"f": textTerms("b", "a")}),
		newTestMatch(t, []string{"f"}, map[string][][]byte{"f": textTerms("a")}),
		newTestMatch(t, []string{"f"}, map[string][][]byte{"f": numTerms(5)}),
		newTestMatch(t, []string{"f"}, map[string][][]byte{}),
	}
	agg := NewTermsAggregation(search.Field("f"), TextValueSource, 10)
	calc := agg.Calculator().(*TermsCalculator)
	want := map[string]int{}
	for _, d := range docs {
		calc.Consume(d)
		want[string(search.Field("f").Value(d))]++
	}
	calc.Finish()
	got := map[string]int{}
	for _, b := range calc.Buckets() {
		got[b.Name()] = int(b.Aggregations()["count"].(search.MetricCalculator).Value())
	}
	assert.Equal(t, want, got)
	assert.Len(t, got, 4) // "b", "a", the numeric term and missing ""
}

func TestTermsCalculatorValueSources(t *testing.T) {
	docs := func(t *testing.T, vals ...[][]byte) []*search.DocumentMatch {
		rv := make([]*search.DocumentMatch, 0, len(vals))
		for _, v := range vals {
			rv = append(rv, newTestMatch(t, []string{"f"}, map[string][][]byte{"f": v}))
		}
		return rv
	}
	tests := []struct {
		name    string
		srcType int
		vals    [][][]byte
		want    map[string]uint64
	}{
		{
			name: "text values", srcType: TextValuesSource,
			vals: [][][]byte{textTerms("a", "b"), textTerms("b")},
			want: map[string]uint64{"a": 1, "b": 2},
		},
		{
			name: "numeric value", srcType: NumericValueSource,
			vals: [][][]byte{numTerms(1.5), numTerms(1.5), numTerms(3)},
			want: map[string]uint64{"1.5": 2, "3": 1},
		},
		{
			name: "numeric values", srcType: NumericValuesSource,
			vals: [][][]byte{numTerms(1, 2), numTerms(2)},
			want: map[string]uint64{"1": 1, "2": 2},
		},
		{
			name: "boolean value", srcType: BooleanValueSource,
			vals: [][][]byte{numTerms(1), numTerms(0), numTerms(1)},
			want: map[string]uint64{"true": 2, "false": 1},
		},
		{
			name: "boolean values", srcType: BooleanValuesSource,
			vals: [][][]byte{numTerms(0, 1), numTerms(0)},
			want: map[string]uint64{"true": 1, "false": 2},
		},
		{
			name: "unsupported type", srcType: -1,
			vals: [][][]byte{textTerms("a")},
			want: map[string]uint64{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewTermsAggregation(search.Field("f"), tt.srcType, 10).Calculator().(*TermsCalculator)
			for _, d := range docs(t, tt.vals...) {
				calc.Consume(d)
			}
			calc.Finish()
			assert.Equal(t, tt.want, bucketCounts(calc.Buckets()))
		})
	}
}

func TestTermsCalculatorSizeAndOther(t *testing.T) {
	calc := NewTermsAggregation(search.Field("city"), TextValueSource, 1).Calculator().(*TermsCalculator)
	for _, d := range filterDocs(t, "sf", "sf", "la") {
		calc.Consume(d)
	}
	calc.Finish()
	assert.Equal(t, []string{"sf"}, bucketNames(calc.Buckets()))
	assert.Equal(t, 1, calc.Other())
}

func TestTermsCalculatorMerge(t *testing.T) {
	agg := NewTermsAggregation(search.Field("city"), TextValueSource, 2)
	a := agg.Calculator().(*TermsCalculator)
	b := agg.Calculator().(*TermsCalculator)
	for _, d := range filterDocs(t, "sf", "la") {
		a.Consume(d)
	}
	for _, d := range filterDocs(t, "la", "la", "nyc") {
		b.Consume(d)
	}
	a.Finish()
	b.Finish()
	a.Merge(b)
	assert.Equal(t, []string{"la", "sf"}, bucketNames(a.Buckets()))
	assert.Equal(t, map[string]uint64{"la": 3, "sf": 1}, bucketCounts(a.Buckets()))
	assert.Equal(t, 1, a.Other())

	// other calculator types are ignored
	a.Merge(NewMissingAggregation(search.Field("city")).Calculator())
	assert.Len(t, a.Buckets(), 2)
}

func TestTermsCalculatorKeyFilter(t *testing.T) {
	tests := []struct {
		name  string
		keep  func(string) bool
		want  map[string]uint64
		other int
	}{
		{name: "no filter trims the rare key", want: map[string]uint64{"sf": 2}, other: 1},
		{name: "kept rare key is not displaced", keep: func(k string) bool { return k == "la" },
			want: map[string]uint64{"la": 1}, other: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewTermsAggregation(search.Field("city"), TextValueSource, 1)
			if tt.keep != nil {
				agg.SetKeyFilter(tt.keep)
			}
			calc := agg.Calculator().(*TermsCalculator)
			for _, d := range filterDocs(t, "sf", "sf", "la") {
				calc.Consume(d)
			}
			calc.Finish()
			assert.Equal(t, tt.want, bucketCounts(calc.Buckets()))
			assert.Equal(t, tt.other, calc.Other())
		})
	}

	t.Run("merged keys are filtered", func(t *testing.T) {
		agg := NewTermsAggregation(search.Field("city"), TextValueSource, 1)
		agg.SetKeyFilter(func(k string) bool { return k != "sf" })
		a, b := agg.Calculator().(*TermsCalculator), agg.Calculator().(*TermsCalculator)
		a.Consume(filterDocs(t, "la")[0])
		for _, d := range filterDocs(t, "sf", "sf") {
			b.Consume(d)
		}
		a.Finish()
		b.Finish()
		a.Merge(b)
		assert.Equal(t, map[string]uint64{"la": 1}, bucketCounts(a.Buckets()))
	})
}

func TestTermsAggregationFields(t *testing.T) {
	agg := NewTermsAggregation(search.Field("city"), TextValueSource, 10)
	agg.AddAggregation("avg_age", aggregations.Avg(search.Field("age")))
	assert.ElementsMatch(t, []string{"city", "age"}, agg.Fields())
}
