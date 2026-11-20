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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func filterMappings() *meta.Mappings {
	mappings := advancedMappings()
	mappings.SetProperty("ts", meta.NewProperty("date"))
	mappings.SetProperty("flag", meta.NewProperty("bool"))
	return mappings
}

type obj = map[string]interface{}

func TestFilterPredicateMatches(t *testing.T) {
	docs := cityDocs(t,
		[]string{"1", "2", "3"},
		[]string{"sf", "la", "nyc"},
		[]float64{10, 20, 30},
	)
	tests := []struct {
		name  string
		query interface{}
		want  int
	}{
		{name: "several clauses are and-ed", query: obj{"term": obj{"city": "sf"}, "range": obj{"age": obj{"gte": 0}}}, want: 1},
		{name: "several clauses, one fails", query: obj{"exists": obj{"field": "city"}, "term": obj{"city": "zz"}}, want: 0},
		{name: "term on a numeric field", query: obj{"term": obj{"age": 20}}, want: 1},
		{name: "term long form on a numeric field", query: obj{"term": obj{"age": obj{"value": 30}}}, want: 1},
		{name: "term on a numeric field misses", query: obj{"term": obj{"age": 25}}, want: 0},
		{name: "term ignores boost", query: obj{"term": obj{"boost": 2, "city": "sf"}}, want: 1},
		{name: "terms on a numeric field", query: obj{"terms": obj{"age": []interface{}{10, 30}}}, want: 2},
		{name: "terms with a single value", query: obj{"terms": obj{"city": "la"}}, want: 1},
		{name: "range gt", query: obj{"range": obj{"age": obj{"gt": 10}}}, want: 2},
		{name: "range lt", query: obj{"range": obj{"age": obj{"lt": 30}}}, want: 2},
		{name: "range lte with boost", query: obj{"range": obj{"age": obj{"lte": 20, "boost": 1}}}, want: 2},
		{name: "range gte lt", query: obj{"range": obj{"age": obj{"gte": 10, "lt": 20}}}, want: 1},
		{name: "bool filter", query: obj{"bool": obj{"filter": obj{"term": obj{"city": "nyc"}}}}, want: 1},
		{name: "bool must fails", query: obj{"bool": obj{"must": obj{"term": obj{"city": "zz"}}}}, want: 0},
		{
			name: "bool minimum_should_match",
			query: obj{"bool": obj{
				"should":               []interface{}{obj{"term": obj{"city": "sf"}}, obj{"range": obj{"age": obj{"lte": 10}}}},
				"minimum_should_match": 2,
			}},
			want: 1,
		},
		{
			name: "bool minimum_should_match as a string",
			query: obj{"bool": obj{
				"should":               []interface{}{obj{"term": obj{"city": "sf"}}, obj{"term": obj{"city": "la"}}},
				"minimum_should_match": "1",
			}},
			want: 2,
		},
		{name: "typed query", query: map[string]map[string]string{"term": {"city": "sf"}}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred, err := FilterPredicate("filter", tt.query, filterMappings())
			require.NoError(t, err)
			got := 0
			for _, d := range docs {
				if pred.Match(d) {
					got++
				}
			}
			assert.Equal(t, tt.want, got)
			assert.NotEmpty(t, pred.Fields)
		})
	}
}

func TestFilterPredicateErrors(t *testing.T) {
	tests := []struct {
		name  string
		query interface{}
		want  string
	}{
		{name: "nil query", query: nil, want: "aggregation needs a query"},
		{name: "query does not marshal", query: map[string]chan int{"a": make(chan int)}, want: "aggregation query parse err"},
		{name: "query is not an object", query: "term", want: "aggregation query parse err"},
		{name: "exists format", query: obj{"exists": "city"}, want: "exists query format error"},
		{name: "exists needs a field", query: obj{"exists": obj{}}, want: "exists query needs a field"},
		{name: "term format", query: obj{"term": "city"}, want: "term query format error"},
		{name: "term needs a field", query: obj{"term": obj{"boost": 1}}, want: "term query needs a field"},
		{name: "term needs a value", query: obj{"term": obj{"city": obj{"query": "sf"}}}, want: "term query needs value"},
		{name: "match needs a query", query: obj{"match": obj{"city": obj{"value": "sf"}}}, want: "match query needs query"},
		{name: "term numeric value", query: obj{"term": obj{"age": "abc"}}, want: "query value for [age] must be numeric"},
		{name: "terms format", query: obj{"terms": "city"}, want: "terms query format error"},
		{name: "terms needs values", query: obj{"terms": obj{"city": []interface{}{}}}, want: "terms query needs values"},
		{name: "range format", query: obj{"range": "age"}, want: "range query format error"},
		{name: "range bounds format", query: obj{"range": obj{"age": 5}}, want: "range query format error"},
		{name: "range needs a bound", query: obj{"range": obj{"age": obj{"boost": 1}}}, want: "range query needs a bound"},
		{name: "range bound value", query: obj{"range": obj{"age": obj{"gte": "abc"}}}, want: "range value for [age] must be numeric"},
		{name: "bool format", query: obj{"bool": "x"}, want: "bool query format error"},
		{name: "bool must", query: obj{"bool": obj{"must": obj{"prefix": obj{}}}}, want: "[prefix] query"},
		{name: "bool filter", query: obj{"bool": obj{"filter": obj{"prefix": obj{}}}}, want: "[prefix] query"},
		{name: "bool must_not", query: obj{"bool": obj{"must_not": obj{"prefix": obj{}}}}, want: "[prefix] query"},
		{name: "bool should", query: obj{"bool": obj{"should": obj{"prefix": obj{}}}}, want: "[prefix] query"},
		{name: "bool nil clause", query: obj{"bool": obj{"must": []interface{}{nil}}}, want: "aggregation needs a query"},
		{
			name:  "bool minimum_should_match",
			query: obj{"bool": obj{"should": obj{"match_all": obj{}}, "minimum_should_match": "abc"}},
			want:  "minimum_should_match must be numeric",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred, err := FilterPredicate("filter", tt.query, filterMappings())
			require.Error(t, err)
			assert.Nil(t, pred)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestToQueryNumber(t *testing.T) {
	ts := time.Date(2022, 1, 2, 3, 4, 5, 0, time.UTC)
	day := meta.NewProperty("date")
	day.Format = "2006-01-02"
	tests := []struct {
		name    string
		v       interface{}
		prop    meta.Property
		want    float64
		wantErr string
	}{
		{name: "date string", v: ts.Format(time.RFC3339), prop: meta.NewProperty("date"), want: float64(ts.UnixNano())},
		{name: "date with format", v: "2022-01-02", prop: day, want: float64(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC).UnixNano())},
		{name: "date bad string", v: "yesterday", prop: day, wantErr: "must be a date of format 2006-01-02"},
		{name: "date epoch millis", v: 1000, prop: meta.NewProperty("time"), want: float64(time.Second)},
		{name: "date bad type", v: []int{1}, prop: meta.NewProperty("date"), wantErr: "must be a date or a timestamp"},
		{name: "bool true", v: true, prop: meta.NewProperty("bool"), want: 1},
		{name: "bool false", v: false, prop: meta.NewProperty("boolean"), want: 0},
		{name: "bool string true", v: "true", prop: meta.NewProperty("bool"), want: 1},
		{name: "bool string false", v: "false", prop: meta.NewProperty("bool"), want: 0},
		{name: "bool bad string", v: "yes please", prop: meta.NewProperty("bool"), wantErr: "must be a boolean"},
		{name: "bool bad type", v: 1, prop: meta.NewProperty("bool"), wantErr: "must be a boolean"},
		{name: "numeric", v: 5, prop: meta.NewProperty("numeric"), want: 5},
		{name: "numeric bad", v: "x", prop: meta.NewProperty("numeric"), wantErr: "must be numeric"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toQueryNumber(tt.v, tt.prop)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// term / terms on date and bool fields compile through toQueryNumber.
func TestFilterPredicateDateAndBool(t *testing.T) {
	tests := []struct {
		name    string
		query   interface{}
		wantErr string
	}{
		{name: "term on a date", query: obj{"term": obj{"ts": "2022-01-02T03:04:05Z"}}},
		{name: "terms on a bool", query: obj{"terms": obj{"flag": []interface{}{true, "false"}}}},
		{name: "range on a date", query: obj{"range": obj{"ts": obj{"gte": 0}}}},
		{name: "term on a bad date", query: obj{"term": obj{"ts": "nope"}}, wantErr: "query value for [ts] must be a date"},
		{name: "term on a bad bool", query: obj{"term": obj{"flag": "nope"}}, wantErr: "query value for [flag] must be a boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred, err := FilterPredicate("filter", tt.query, filterMappings())
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			// the fixture docs have no such field
			d := cityDocs(t, []string{"1"}, []string{"sf"}, []float64{1})[0]
			assert.False(t, pred.Match(d))
		})
	}
}
