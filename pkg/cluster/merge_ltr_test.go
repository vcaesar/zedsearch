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

package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestMergeSearchLTRWindow(t *testing.T) {
	ltr := map[string]interface{}{"window_size": 2.0, "learning_to_rank": map[string]interface{}{"model_id": "m"}}
	// each owner returns its window of 2 scored by the model, then its tail
	// with query scores far above the model scores
	responses := func() []*meta.SearchResponse {
		return []*meta.SearchResponse{
			response(3, -1, 1, hit("a", "a1", -1, nil), hit("a", "a2", -3, nil), hit("a", "a3", 9, nil)),
			response(3, -2, 1, hit("b", "b1", -2, nil), hit("b", "b2", -4, nil), hit("b", "b3", 8, nil)),
		}
	}
	tests := []struct {
		name      string
		query     *meta.ZedQuery
		wantIDs   []string
		wantScore float64
	}{
		{"windows stay on top", &meta.ZedQuery{Size: 10, Rescore: ltr},
			[]string{"a1", "b1", "a2", "b2", "a3", "b3"}, -1},
		{"page across the windows", &meta.ZedQuery{From: 3, Size: 2, Rescore: []interface{}{ltr}},
			[]string{"b2", "a3"}, -1},
		{"query rescorer last merges by score", &meta.ZedQuery{Size: 10, Rescore: []interface{}{ltr,
			map[string]interface{}{"query": map[string]interface{}{"rescore_query": map[string]interface{}{"match_all": map[string]interface{}{}}}}}},
			[]string{"a3", "b3", "a1", "b1", "a2", "b2"}, -1},
		{"sltr combines, merges by score", &meta.ZedQuery{Size: 10, Rescore: map[string]interface{}{"query": map[string]interface{}{
			"rescore_query": map[string]interface{}{"sltr": map[string]interface{}{"model": "m"}}}}},
			[]string{"a3", "b3", "a1", "b1", "a2", "b2"}, -1},
		{"invalid rescore merges by score", &meta.ZedQuery{Size: 10, Rescore: 1.0},
			[]string{"a3", "b3", "a1", "b1", "a2", "b2"}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeSearch(responses(), tt.query)
			assert.Equal(t, tt.wantIDs, hitIDs(got.Hits.Hits))
			assert.Equal(t, tt.wantScore, got.Hits.MaxScore, "negative max scores are kept")
		})
	}
}

func TestMergeSearchMaxScore(t *testing.T) {
	got := MergeSearch([]*meta.SearchResponse{
		response(0, 0, 1),
		response(1, -2, 1, hit("a", "a1", -2, nil)),
		response(1, -1, 1, hit("b", "b1", -1, nil)),
	}, &meta.ZedQuery{Size: 10})
	assert.Equal(t, -1.0, got.Hits.MaxScore, "an owner without hits does not count")

	got = MergeSearch([]*meta.SearchResponse{response(0, 0, 1)}, &meta.ZedQuery{Size: 10})
	assert.Equal(t, 0.0, got.Hits.MaxScore)
}

func TestLTRWindow(t *testing.T) {
	assert.Equal(t, 0, ltrWindow(nil))
	assert.Equal(t, 0, ltrWindow(&meta.ZedQuery{}))
	assert.Equal(t, 10, ltrWindow(&meta.ZedQuery{Rescore: map[string]interface{}{"learning_to_rank": map[string]interface{}{"model_id": "m"}}}),
		"default window_size")
}
