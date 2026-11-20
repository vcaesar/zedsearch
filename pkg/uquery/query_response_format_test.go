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

package uquery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

// stubAgg registers a fixed calculator in a bucket.
type stubAgg struct{ calc search.Calculator }

func (a stubAgg) Fields() []string              { return nil }
func (a stubAgg) Calculator() search.Calculator { return a.calc }

// plainCalc is a calculator Response doesn't know how to render.
type plainCalc struct{}

func (plainCalc) Consume(*search.DocumentMatch) {}
func (plainCalc) Finish()                       {}
func (plainCalc) Merge(search.Calculator)       {}

// valueCalc is a metric calculator with a fixed value.
type valueCalc struct {
	plainCalc
	v float64
}

func (c valueCalc) Value() float64 { return c.v }

func stubBucket(calcs map[string]search.Calculator) *search.Bucket {
	aggs := make(map[string]search.Aggregation, len(calcs))
	for name, calc := range calcs {
		aggs[name] = stubAgg{calc: calc}
	}
	return search.NewBucket("test", aggs)
}

func TestFormatResponse(t *testing.T) {
	avgAgg := map[string]meta.Aggregations{"avg": {Avg: &meta.AggregationMetric{Field: "age"}}}

	t.Run("no aggregations", func(t *testing.T) {
		resp := &meta.SearchResponse{}
		assert.NoError(t, FormatResponse(resp, &meta.ZedQuery{}, nil))
		assert.Nil(t, resp.Aggregations)
	})

	t.Run("standard aggregations are dropped", func(t *testing.T) {
		resp := &meta.SearchResponse{}
		bucket := stubBucket(map[string]search.Calculator{
			"count":     valueCalc{v: 3},
			"duration":  valueCalc{v: 1},
			"max_score": valueCalc{v: 2},
			"avg":       valueCalc{v: 42},
		})
		require.NoError(t, FormatResponse(resp, &meta.ZedQuery{Aggregations: avgAgg}, bucket))
		assert.Equal(t, map[string]meta.AggregationResponse{"avg": {Value: 42.0}}, resp.Aggregations)
	})

	t.Run("empty bucket", func(t *testing.T) {
		resp := &meta.SearchResponse{}
		require.NoError(t, FormatResponse(resp, &meta.ZedQuery{Aggregations: avgAgg}, stubBucket(nil)))
		assert.Empty(t, resp.Aggregations)
	})

	t.Run("unsupported calculator", func(t *testing.T) {
		resp := &meta.SearchResponse{}
		err := FormatResponse(resp, &meta.ZedQuery{Aggregations: avgAgg}, stubBucket(map[string]search.Calculator{"avg": plainCalc{}}))
		assert.ErrorContains(t, err, "aggregation doesn't support")
	})

	t.Run("pipelines are left to the caller", func(t *testing.T) {
		resp := &meta.SearchResponse{}
		q := &meta.ZedQuery{Aggregations: map[string]meta.Aggregations{
			"avg_sales": {AvgBucket: &meta.AggregationPipeline{BucketsPath: "missing>sales"}},
		}}
		require.NoError(t, FormatResponse(resp, q, stubBucket(map[string]search.Calculator{"count": valueCalc{v: 1}})))
		assert.Empty(t, resp.Aggregations)
	})
}
