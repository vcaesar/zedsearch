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

package template

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestRequest(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		tpl, err := Request(nil)
		assert.NoError(t, err)
		assert.Nil(t, tpl)
	})

	t.Run("full template", func(t *testing.T) {
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(`{
			"name": "logs",
			"data_stream": {},
			"index_patterns": ["logs-*", "app-*"],
			"priority": 10,
			"template": {
				"settings": {"number_of_shards": 2},
				"mappings": {"properties": {"status": {"type": "keyword"}}}
			}
		}`), &data))
		tpl, err := Request(data)
		require.NoError(t, err)
		assert.Equal(t, []string{"logs-*", "app-*"}, tpl.IndexPatterns)
		assert.Equal(t, 10, tpl.Priority)
		require.NotNil(t, tpl.Template.Settings)
		assert.Equal(t, int64(2), tpl.Template.Settings.NumberOfShards)
		_, ok := tpl.Template.Mappings.GetProperty("status")
		assert.True(t, ok)
	})

	t.Run("priority types", func(t *testing.T) {
		tests := []struct {
			name     string
			priority interface{}
			want     int
		}{
			{"string", "150", 150},
			{"invalid string", "x", 0},
			{"float64", float64(7), 7},
			{"int", 3, 3},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tpl, err := Request(map[string]interface{}{"template": "filebeat-*", "priority": tt.priority})
				require.NoError(t, err)
				assert.Equal(t, tt.want, tpl.Priority)
			})
		}
	})

	t.Run("string template is a pattern", func(t *testing.T) {
		tpl, err := Request(map[string]interface{}{"template": "filebeat-7.16.3-*", "Priority": 150.0})
		require.NoError(t, err)
		assert.Equal(t, []string{"filebeat-7.16.3-*"}, tpl.IndexPatterns)
		assert.Equal(t, 150, tpl.Priority)
		assert.Nil(t, tpl.Template.Settings)
		assert.Nil(t, tpl.Template.Mappings)
	})

	t.Run("empty template object", func(t *testing.T) {
		tpl, err := Request(map[string]interface{}{"index_patterns": []interface{}{"a-*"}, "template": map[string]interface{}{}})
		require.NoError(t, err)
		assert.Equal(t, []string{"a-*"}, tpl.IndexPatterns)
		assert.Nil(t, tpl.Template.Settings)
		assert.Nil(t, tpl.Template.Mappings)
	})

	t.Run("errors", func(t *testing.T) {
		settings := map[string]interface{}{"settings": map[string]interface{}{}}
		tests := []struct {
			name string
			data map[string]interface{}
			want string
		}{
			{"missing template", map[string]interface{}{"index_patterns": []interface{}{"a"}}, "template should be defined"},
			{"patterns not array", map[string]interface{}{"template": "a", "index_patterns": "a"}, "index_patterns value should be an array"},
			{"bad priority", map[string]interface{}{"template": "a", "priority": true}, "priority value should be a numberic"},
			{"bad template type", map[string]interface{}{"template": 1}, "template value should be an object"},
			{"bad index", map[string]interface{}{"template": map[string]interface{}{"settings": 1}}, "settings should be an object"},
			{"unknown option", map[string]interface{}{"template": "a", "nope": 1}, "unknown option [nope]"},
			{"no patterns", map[string]interface{}{"template": settings}, "index_patterns should be defined"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tpl, err := Request(tt.data)
				assert.Nil(t, tpl)
				assert.ErrorContains(t, err, tt.want)
			})
		}
	})
}
