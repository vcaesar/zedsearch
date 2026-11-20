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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zedaggregation "github.com/vcaesar/zedsearch/pkg/bluge/aggregation"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestRequestSignificantTerms(t *testing.T) {
	mappings := meta.NewMappings()
	for field, typ := range map[string]string{"body": "text", "tag": "keyword", "n": "numeric", "ok": "bool", "ts": "date"} {
		mappings.SetProperty(field, meta.NewProperty(typ))
	}
	sub := map[string]meta.Aggregations{"max_n": {Max: &meta.AggregationMetric{Field: "n"}}}
	tests := []struct {
		name    string
		agg     meta.Aggregations
		wantErr string
	}{
		{name: "keyword", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}}},
		{name: "numeric with sub aggregations", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "n", Size: 50}, Aggregations: sub}},
		{name: "bool", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "ok"}}},
		{name: "significant_text", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "body"}}},
		{name: "unsupported type", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "ts"}},
			wantErr: "[significant_terms] aggregation doesn't support values of type: [ts:[date]]"},
		{name: "significant_text unmapped", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "nope"}},
			wantErr: "[significant_text] aggregation doesn't support values of type"},
		{name: "bad sub aggregation", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}, Aggregations: globalSub},
			wantErr: "[global]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newTestSearchAggregation()
			err := Request(req, map[string]meta.Aggregations{"sig": tt.agg}, mappings)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.IsType(t, &zedaggregation.TermsAggregation{}, req.aggs["sig"])
		})
	}
}

func TestSignificantTermsSizes(t *testing.T) {
	tests := []struct {
		size, shardSize   int
		wantReq, wantResp int
	}{
		{0, 0, 100, 10},
		{30, 0, 150, 30},
		{5, 500, 500, 5},
	}
	for _, tt := range tests {
		a := &meta.AggregationSignificantTerms{Size: tt.size, ShardSize: tt.shardSize}
		assert.Equal(t, tt.wantReq, a.RequestSize())
		assert.Equal(t, tt.wantResp, a.ResponseSize())
	}
}
