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

package uquery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransform_ReplacementChildren(t *testing.T) {
	mlt := map[string]interface{}{"more_like_this": map[string]interface{}{"like": "fox"}}
	rewrittenMLT := map[string]interface{}{"match": map[string]interface{}{"title": "fox"}}
	fns := map[string]TransformFunc{
		"rule": func(body map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"pinned": map[string]interface{}{"ids": []interface{}{"1"}, "organic": body["organic"]}}, nil
		},
		"more_like_this": func(map[string]interface{}) (map[string]interface{}, error) {
			return rewrittenMLT, nil
		},
	}
	tests := []struct {
		name  string
		query map[string]interface{}
		want  interface{}
	}{
		{
			name:  "organic of a rule",
			query: map[string]interface{}{"rule": map[string]interface{}{"organic": mlt}},
			want:  map[string]interface{}{"pinned": map[string]interface{}{"ids": []interface{}{"1"}, "organic": rewrittenMLT}},
		},
		{
			name:  "rule nested in bool",
			query: map[string]interface{}{"bool": map[string]interface{}{"must": map[string]interface{}{"rule": map[string]interface{}{"organic": mlt}}}},
			want: map[string]interface{}{"bool": map[string]interface{}{"must": map[string]interface{}{
				"pinned": map[string]interface{}{"ids": []interface{}{"1"}, "organic": rewrittenMLT},
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Transform(tt.query, fns)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("replacement that is itself a matching clause", func(t *testing.T) {
		// an unmatched rule gives back its organic query unchanged
		unmatched := map[string]TransformFunc{
			"rule": func(body map[string]interface{}) (map[string]interface{}, error) {
				return body["organic"].(map[string]interface{}), nil
			},
			"more_like_this": fns["more_like_this"],
		}
		nested := map[string]interface{}{"rule": map[string]interface{}{"organic": map[string]interface{}{"rule": map[string]interface{}{"organic": mlt}}}}
		got, err := Transform(nested, unmatched)
		require.NoError(t, err)
		assert.Equal(t, rewrittenMLT, got)
	})
}
