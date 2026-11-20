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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func u(v uint64) *uint64 { return &v }

func TestToFloat(t *testing.T) {
	tests := []struct {
		name    string
		v       interface{}
		want    float64
		wantErr string
	}{
		{name: "float64", v: 1.5, want: 1.5},
		{name: "float32", v: float32(2.5), want: 2.5},
		{name: "int", v: 3, want: 3},
		{name: "int64", v: int64(4), want: 4},
		{name: "uint64", v: uint64(5), want: 5},
		{name: "true", v: true, want: 1},
		{name: "false", v: false, want: 0},
		{name: "string", v: "6.5", want: 6.5},
		{name: "bad string", v: "x", wantErr: "not a number: x"},
		{name: "other type", v: []int{1}, wantErr: "not a number: []int"},
		{name: "nil", v: nil, wantErr: "not a number: <nil>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toFloat(tt.v)
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

func TestMetricValue(t *testing.T) {
	full := meta.AggregationResponse{
		Count: u(4), Min: f(1), Max: f(9), Avg: f(5), Sum: f(20), DocCount: u(7), Value: 3,
	}
	tests := []struct {
		name   string
		r      meta.AggregationResponse
		metric string
		want   float64
		ok     bool
	}{
		{name: "count", r: full, metric: "count", want: 4, ok: true},
		{name: "min", r: full, metric: "min", want: 1, ok: true},
		{name: "max", r: full, metric: "max", want: 9, ok: true},
		{name: "avg", r: full, metric: "avg", want: 5, ok: true},
		{name: "sum", r: full, metric: "sum", want: 20, ok: true},
		{name: "doc_count", r: full, metric: "doc_count", want: 7, ok: true},
		{name: "value", r: full, metric: "value", want: 3, ok: true},
		{name: "default value", r: full, want: 3, ok: true},
		{name: "value is not a number", r: meta.AggregationResponse{Value: "x"}},
		{name: "single bucket doc_count", r: meta.AggregationResponse{DocCount: u(2)}, want: 2, ok: true},
		{name: "missing count", metric: "count"},
		{name: "missing min", metric: "min"},
		{name: "missing max", metric: "max"},
		{name: "missing avg", metric: "avg"},
		{name: "missing sum", metric: "sum"},
		{name: "missing doc_count", metric: "doc_count"},
		{name: "missing value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := metricValue(tt.r, tt.metric)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBucketValuePaths(t *testing.T) {
	bucket := map[string]interface{}{
		"key":       int64(42),
		"doc_count": uint64(3),
		"bay": meta.AggregationResponse{DocCount: u(2), Aggregations: map[string]meta.AggregationResponse{
			"stats": {Avg: f(1.5)},
		}},
		"plain": meta.AggregationResponse{Value: 1.0},
	}
	tests := []struct {
		path string
		want float64
		ok   bool
	}{
		{path: "_key", want: 42, ok: true},
		{path: "_count", want: 3, ok: true},
		{path: "bay>stats.avg", want: 1.5, ok: true},
		{path: "bay", want: 2, ok: true},
		{path: "nope>stats", ok: false},
		{path: "plain>x", ok: false},
		{path: "missing", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := bucketValue(bucket, tt.path)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSiblingResponseOrdering(t *testing.T) {
	values := []float64{3, 1, 5, 2}
	assert.Equal(t, 1.0, siblingResponse("min_bucket", values).Value)
	assert.Equal(t, 5.0, siblingResponse("max_bucket", values).Value)
	stats := siblingResponse("stats_bucket", values)
	assert.Equal(t, 1.0, *stats.Min)
	assert.Equal(t, 5.0, *stats.Max)
	assert.Equal(t, 2.75, *stats.Avg)
	for _, kind := range []string{"avg_bucket", "min_bucket", "max_bucket"} {
		assert.Nil(t, siblingResponse(kind, nil).Value, kind)
	}
}

func TestFindBucketsThroughSingleBucket(t *testing.T) {
	resp := map[string]meta.AggregationResponse{
		"bay": {DocCount: u(4), Aggregations: salesResponse(f(10), f(30))},
	}
	require.NoError(t, ApplyPipelines(resp, map[string]meta.Aggregations{
		"total": {SumBucket: &meta.AggregationPipeline{BucketsPath: "bay>sales_per_month>sales"}},
	}))
	assert.Equal(t, 40.0, resp["total"].Value)
}

func TestTruthy(t *testing.T) {
	tests := []struct {
		name    string
		v       interface{}
		want    bool
		wantErr bool
	}{
		{name: "true", v: true, want: true},
		{name: "false", v: false},
		{name: "non zero", v: 2.0, want: true},
		{name: "zero", v: 0},
		{name: "not a number", v: []int{1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := truthy(tt.v)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMovingFunctionWindows(t *testing.T) {
	values := []float64{3, 1, 4, 1, 5}
	tests := []struct {
		script string
		want   float64
	}{
		{script: "MovingFunctions.min(values)", want: 1},
		{script: "MovingFunctions.max(values)", want: 5},
		{script: " MovingFunctions.sum(values) ", want: 14},
	}
	for _, tt := range tests {
		t.Run(tt.script, func(t *testing.T) {
			fn, err := movingFunction(tt.script)
			require.NoError(t, err)
			assert.Equal(t, tt.want, fn(values))
		})
	}

	_, err := movingFunction("MovingFunctions.stdDev(values, 3)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdDev only supports MovingFunctions.unweightedAvg(values)")

	assert.True(t, math.IsNaN(reduceWindow(nil, math.Min)))
	// a shift far before the window leaves every window empty
	assert.Equal(t, []float64{0, 0, 0}, movingFn([]float64{1, 2, 3}, 1, -5, windowSum))
}

func TestSortOrder(t *testing.T) {
	tests := []struct {
		name    string
		order   interface{}
		asc     bool
		wantErr string
	}{
		{name: "asc", order: "asc", asc: true},
		{name: "object desc", order: map[string]interface{}{"order": "desc"}},
		{name: "object asc", order: map[string]interface{}{"order": "asc"}, asc: true},
		{name: "object without order", order: map[string]interface{}{"mode": "avg"}, wantErr: "sort order must be asc or desc"},
		{name: "bad type", order: 1, wantErr: "sort must be a string or an object"},
		{name: "bad value", order: "up", wantErr: "got [up]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asc, err := sortOrder(tt.order)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.asc, asc)
		})
	}
}

func TestApplyBucketScriptEdgeCases(t *testing.T) {
	t.Run("missing path is skipped", func(t *testing.T) {
		resp := salesResponse(f(10), nil)
		require.NoError(t, ApplyPipelines(resp, histogramAggs(map[string]meta.Aggregations{
			"double": {BucketScript: &meta.AggregationBucketScript{
				BucketsPath: map[string]string{"s": "sales"},
				Script:      "params.s * 2",
			}},
		})))
		got := bucketValues(t, resp, "sales_per_month", "double")
		assert.Equal(t, 20.0, got[0])
		assert.True(t, math.IsNaN(got[1]))
	})

	t.Run("missing path is zero with insert_zeros", func(t *testing.T) {
		resp := salesResponse(f(10), nil)
		require.NoError(t, ApplyPipelines(resp, histogramAggs(map[string]meta.Aggregations{
			"plus": {BucketScript: &meta.AggregationBucketScript{
				BucketsPath: map[string]string{"s": "sales"},
				Script:      "params.s + 1",
				GapPolicy:   gapInsertZeros,
			}},
		})))
		assert.Equal(t, []float64{11, 1}, bucketValues(t, resp, "sales_per_month", "plus"))
	})

	t.Run("bucket_selector keeps a truthy number", func(t *testing.T) {
		resp := salesResponse(f(0), f(30), nil)
		require.NoError(t, ApplyPipelines(resp, histogramAggs(map[string]meta.Aggregations{
			"keep": {BucketSelector: &meta.AggregationBucketScript{
				BucketsPath: map[string]string{"s": "sales"},
				Script:      "params.s",
			}},
		})))
		assert.Equal(t, []string{"2022-02"}, bucketKeys(t, resp, "sales_per_month"))
	})
}

func TestApplyPipelinesSerialDiffDefaultLag(t *testing.T) {
	resp := salesResponse(f(10), f(30), f(25))
	require.NoError(t, ApplyPipelines(resp, histogramAggs(map[string]meta.Aggregations{
		"diff": {SerialDiff: &meta.AggregationSerialDiff{AggregationPipeline: meta.AggregationPipeline{BucketsPath: "sales"}}},
	})))
	got := bucketValues(t, resp, "sales_per_month", "diff")
	assert.True(t, math.IsNaN(got[0]))
	assert.Equal(t, []float64{20, -5}, got[1:])
}

func TestApplyBucketSortEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		sort *meta.AggregationBucketSort
		want []string
	}{
		{
			name: "missing values sort last",
			sort: &meta.AggregationBucketSort{Sort: []map[string]interface{}{{"sales": "asc"}}},
			want: []string{"2022-02", "2022-04", "2022-01", "2022-03"},
		},
		{
			name: "insert_zeros sorts missing values as zero",
			sort: &meta.AggregationBucketSort{Sort: []map[string]interface{}{{"sales": "asc"}}, GapPolicy: gapInsertZeros},
			want: []string{"2022-01", "2022-03", "2022-02", "2022-04"},
		},
		{
			name: "ties fall through to the next key",
			sort: &meta.AggregationBucketSort{Sort: []map[string]interface{}{
				{"sales": map[string]interface{}{"order": "desc"}},
				{"_count": "desc"},
			}},
			want: []string{"2022-04", "2022-02", "2022-03", "2022-01"},
		},
		{
			name: "from past the end",
			sort: &meta.AggregationBucketSort{From: 10},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := salesResponse(nil, f(10), nil, f(10))
			require.NoError(t, ApplyPipelines(resp, histogramAggs(map[string]meta.Aggregations{"sorted": {BucketSort: tt.sort}})))
			assert.Equal(t, tt.want, bucketKeys(t, resp, "sales_per_month"))
		})
	}
}

func TestApplyPipelinesMoreErrors(t *testing.T) {
	selector := func(p *meta.AggregationBucketScript) map[string]meta.Aggregations {
		return histogramAggs(map[string]meta.Aggregations{"result": {BucketSelector: p}})
	}
	script := func(p *meta.AggregationBucketScript) map[string]meta.Aggregations {
		return histogramAggs(map[string]meta.Aggregations{"result": {BucketScript: p}})
	}
	sales := map[string]string{"s": "sales"}
	tests := []struct {
		name string
		aggs map[string]meta.Aggregations
		want string
	}{
		{
			name: "cumulative_sum gap_policy",
			aggs: histogramAggs(map[string]meta.Aggregations{"result": {CumulativeSum: &meta.AggregationPipeline{BucketsPath: "sales", GapPolicy: "nope"}}}),
			want: "[cumulative_sum] aggregation gap_policy must be",
		},
		{
			name: "derivative needs buckets_path",
			aggs: histogramAggs(map[string]meta.Aggregations{"result": {Derivative: &meta.AggregationPipeline{}}}),
			want: "[derivative] aggregation needs buckets_path",
		},
		{
			name: "bucket_script gap_policy",
			aggs: script(&meta.AggregationBucketScript{BucketsPath: sales, Script: "1", GapPolicy: "nope"}),
			want: "[bucket_script] aggregation gap_policy must be",
		},
		{
			name: "bucket_script needs buckets_path",
			aggs: script(&meta.AggregationBucketScript{Script: "1"}),
			want: "[bucket_script] aggregation needs buckets_path",
		},
		{
			name: "bucket_script run error",
			aggs: script(&meta.AggregationBucketScript{BucketsPath: sales, Script: "[1][int(params.s)]"}),
			want: "[bucket_script] aggregation script run err",
		},
		{
			name: "bucket_script must return a number",
			aggs: script(&meta.AggregationBucketScript{BucketsPath: sales, Script: `"abc"`}),
			want: "script must return a number, got string",
		},
		{
			name: "bucket_selector compile error",
			aggs: selector(&meta.AggregationBucketScript{BucketsPath: sales, Script: "params.s >"}),
			want: "[bucket_selector] aggregation script compile err",
		},
		{
			name: "bucket_selector run error",
			aggs: selector(&meta.AggregationBucketScript{BucketsPath: sales, Script: "[true][int(params.s)]"}),
			want: "[bucket_selector] aggregation script run err",
		},
		{
			name: "bucket_selector must return a boolean",
			aggs: selector(&meta.AggregationBucketScript{BucketsPath: sales, Script: "[1]"}),
			want: "script must return a boolean",
		},
		{
			name: "bucket_sort gap_policy",
			aggs: histogramAggs(map[string]meta.Aggregations{"result": {BucketSort: &meta.AggregationBucketSort{GapPolicy: "nope"}}}),
			want: "[bucket_sort] aggregation gap_policy must be",
		},
		{
			name: "bucket_sort negative from",
			aggs: histogramAggs(map[string]meta.Aggregations{"result": {BucketSort: &meta.AggregationBucketSort{From: -1}}}),
			want: "from and size must not be negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ApplyPipelines(salesResponse(f(10), f(30)), tt.aggs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestApplyPipelinesTraversal(t *testing.T) {
	badSub := map[string]meta.Aggregations{
		"sales":  {Sum: &meta.AggregationMetric{Field: "price"}},
		"result": {Derivative: &meta.AggregationPipeline{}},
	}
	cumsum := map[string]meta.Aggregations{
		"sales":  {Sum: &meta.AggregationMetric{Field: "price"}},
		"cumsum": {CumulativeSum: &meta.AggregationPipeline{BucketsPath: "sales"}},
	}

	t.Run("an aggregation without response is skipped", func(t *testing.T) {
		require.NoError(t, ApplyPipelines(map[string]meta.AggregationResponse{}, histogramAggs(cumsum)))
	})

	t.Run("single bucket aggregation", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"bay": {DocCount: u(2), Aggregations: salesResponse(f(1), f(2))}}
		aggs := map[string]meta.Aggregations{"bay": {Filter: obj{"match_all": obj{}}, Aggregations: histogramAggs(cumsum)}}
		require.NoError(t, ApplyPipelines(resp, aggs))
		assert.Equal(t, []float64{1, 3}, bucketValues(t, resp["bay"].Aggregations, "sales_per_month", "cumsum"))
	})

	t.Run("single bucket aggregation error", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"bay": {DocCount: u(2), Aggregations: salesResponse(f(1), f(2))}}
		aggs := map[string]meta.Aggregations{"bay": {Filter: obj{"match_all": obj{}}, Aggregations: histogramAggs(badSub)}}
		err := ApplyPipelines(resp, aggs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "needs buckets_path")
	})

	t.Run("bucket sub aggregation error", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"by_city": {Buckets: []map[string]interface{}{
			{"key": "sf", "doc_count": uint64(2), "sales_per_month": salesResponse(f(1), f(2))["sales_per_month"]},
		}}}
		aggs := map[string]meta.Aggregations{"by_city": {
			Terms:        &meta.AggregationsTerms{Field: "city"},
			Aggregations: histogramAggs(badSub),
		}}
		err := ApplyPipelines(resp, aggs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "needs buckets_path")
	})

	t.Run("keyed buckets skip non responses and leaves", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"by_type": {Buckets: map[string]interface{}{
			"raw":  map[string]interface{}{"doc_count": 1},
			"leaf": meta.AggregationResponse{DocCount: u(1)},
		}}}
		aggs := map[string]meta.Aggregations{"by_type": {Filters: &meta.AggregationFilters{}, Aggregations: histogramAggs(cumsum)}}
		require.NoError(t, ApplyPipelines(resp, aggs))
	})

	t.Run("keyed buckets error", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"by_type": {Buckets: map[string]interface{}{
			"a": meta.AggregationResponse{Aggregations: salesResponse(f(1), f(2))},
		}}}
		aggs := map[string]meta.Aggregations{"by_type": {Filters: &meta.AggregationFilters{}, Aggregations: histogramAggs(badSub)}}
		err := ApplyPipelines(resp, aggs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "needs buckets_path")
	})
}
