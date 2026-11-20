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

package index

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestRequest(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		for _, data := range []map[string]interface{}{nil, {}} {
			index, err := Request(data)
			assert.NoError(t, err)
			assert.Nil(t, index)
		}
	})

	t.Run("errors", func(t *testing.T) {
		tests := []struct {
			name string
			data map[string]interface{}
			want string
		}{
			{"settings not object", map[string]interface{}{"settings": 1}, "settings should be an object"},
			{"settings parse error", map[string]interface{}{"settings": map[string]interface{}{"number_of_shards": "x"}}, "settings parse error"},
			{"analysis error", map[string]interface{}{"settings": map[string]interface{}{"analysis": map[string]interface{}{
				"analyzer": map[string]interface{}{"a": map[string]interface{}{}},
			}}}, "settings.analysis parse error"},
			{"mappings not object", map[string]interface{}{"mappings": 1}, "mappings should be an object"},
			{"mappings error", map[string]interface{}{"mappings": map[string]interface{}{"properties": 1}}, "properties should be an object"},
			{"unknown analyzer in mappings", map[string]interface{}{"mappings": map[string]interface{}{"properties": map[string]interface{}{
				"msg": map[string]interface{}{"type": "text", "analyzer": "my"},
			}}}, "my"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				index, err := Request(tt.data)
				assert.Nil(t, index)
				assert.ErrorContains(t, err, tt.want)
			})
		}
	})

	t.Run("default settings are dropped", func(t *testing.T) {
		index, err := Request(map[string]interface{}{"settings": map[string]interface{}{}})
		require.NoError(t, err)
		assert.Nil(t, index.Settings)
		assert.Nil(t, index.Mappings)
	})

	t.Run("settings analyzers apply to mappings", func(t *testing.T) {
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(`{
			"settings": {"number_of_shards": 3, "analysis": {"analyzer": {"my": {"type": "standard"}}}},
			"mappings": {"properties": {"msg": {"type": "text", "analyzer": "my"}}}
		}`), &data))
		index, err := Request(data)
		require.NoError(t, err)
		require.NotNil(t, index.Settings)
		assert.Equal(t, int64(3), index.Settings.NumberOfShards)
		prop, ok := index.Mappings.GetProperty("msg")
		require.True(t, ok)
		assert.Equal(t, "my", prop.Analyzer)
	})
}
