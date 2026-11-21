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

package meta

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestGUI(t *testing.T) {
	c, w := utils.NewContext()
	GUI(c)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "./ui/", w.Header().Get("Location"))
}

func TestGetHealthz(t *testing.T) {
	c, w := utils.NewContext()
	GetHealthz(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

func TestGetVersion(t *testing.T) {
	c, w := utils.NewContext()
	GetVersion(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var got VersionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, VersionResponse{
		Version:    Version,
		Build:      Build,
		CommitHash: CommitHash,
		Branch:     Branch,
		BuildDate:  BuildDate,
	}, got)
}

func TestAggregationSignificantTerms_Sizes(t *testing.T) {
	tests := []struct {
		name         string
		agg          AggregationSignificantTerms
		wantResponse int
		wantRequest  int
	}{
		{name: "defaults", wantResponse: DefaultSignificantTermsSize, wantRequest: 100},
		{name: "size drives the request", agg: AggregationSignificantTerms{Size: 50}, wantResponse: 50, wantRequest: 250},
		{name: "shard size wins when larger", agg: AggregationSignificantTerms{Size: 50, ShardSize: 500}, wantResponse: 50, wantRequest: 500},
		{name: "negative size falls back", agg: AggregationSignificantTerms{Size: -1}, wantResponse: DefaultSignificantTermsSize, wantRequest: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantResponse, tt.agg.ResponseSize())
			assert.Equal(t, tt.wantRequest, tt.agg.RequestSize())
		})
	}
}

func TestAggregationResponse_MarshalJSON(t *testing.T) {
	count := uint64(3)
	tests := []struct {
		name string
		agg  AggregationResponse
		want string
	}{
		{name: "without sub aggregations", agg: AggregationResponse{Value: 1.5}, want: `{"value":1.5}`},
		{
			name: "sub aggregations are inlined in name order",
			agg: AggregationResponse{DocCount: &count, Aggregations: map[string]AggregationResponse{
				"b": {Value: 2.5},
				"a": {Value: 1.5},
			}},
			want: `{"doc_count":3,"a":{"value":1.5},"b":{"value":2.5}}`,
		},
		{
			name: "only sub aggregations",
			agg:  AggregationResponse{Aggregations: map[string]AggregationResponse{"a": {Value: 1.5}}},
			want: `{"a":{"value":1.5}}`,
		},
		{
			name: "nested sub aggregations",
			agg: AggregationResponse{Aggregations: map[string]AggregationResponse{
				"a": {DocCount: &count, Aggregations: map[string]AggregationResponse{"b": {Value: 2.5}}},
			}},
			want: `{"a":{"doc_count":3,"b":{"value":2.5}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.agg)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}

	t.Run("unsupported value", func(t *testing.T) {
		_, err := AggregationResponse{Value: make(chan int)}.MarshalJSON()
		assert.Error(t, err)
	})

	t.Run("unsupported sub aggregation value", func(t *testing.T) {
		_, err := AggregationResponse{Aggregations: map[string]AggregationResponse{
			"a": {Value: make(chan int)},
		}}.MarshalJSON()
		assert.Error(t, err)
	})
}
