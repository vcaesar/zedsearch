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

package mappings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestRequest_Empty(t *testing.T) {
	for _, data := range []map[string]interface{}{nil, {}} {
		m, err := Request(nil, data)
		assert.NoError(t, err)
		assert.Nil(t, m)
	}
}

func TestRequest_TypeAliases(t *testing.T) {
	tests := []struct {
		typ  string
		want string
	}{
		{"keyword", "keyword"},
		{"constant_keyword", "keyword"},
		{"match_only_text", "text"},
		{"completion", "keyword"},
		{"search_as_you_type", "text"},
		{"rank_feature", "numeric"},
		{"integer", "numeric"},
		{"double", "numeric"},
		{"long", "numeric"},
		{"short", "numeric"},
		{"int", "numeric"},
		{"float", "numeric"},
		{"numeric", "numeric"},
		{"boolean", "bool"},
		{"bool", "bool"},
		{"time", "date"},
		{"datetime", "date"},
		{"date", "date"},
		{"DATE", "date"},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			m, err := Request(nil, map[string]interface{}{"properties": map[string]interface{}{
				"f": map[string]interface{}{"type": tt.typ},
			}})
			require.NoError(t, err)
			p, ok := m.GetProperty("f")
			require.True(t, ok)
			assert.Equal(t, tt.want, p.Type)
		})
	}

	t.Run("ignored types are not mapped", func(t *testing.T) {
		for _, typ := range []string{"flattened", "object", "wildcard", "byte", "alias", "ip", "ip_range", "scaled_float"} {
			m, err := Request(nil, map[string]interface{}{"properties": map[string]interface{}{
				"f": map[string]interface{}{"type": typ},
			}})
			require.NoError(t, err, typ)
			_, ok := m.GetProperty("f")
			assert.False(t, ok, typ)
		}
	})
}

func TestRequest_PropertyOptions(t *testing.T) {
	var props map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{
		"msg": {"type": "text", "analyzer": "standard", "search_analyzer": "simple", "highlightable": true,
			"fields": {"raw": {"type": "keyword"}}},
		"ts": {"type": "date", "format": "2006-01-02", "time_zone": "+08:00", "index": false, "store": true,
			"sortable": false, "aggregatable": false, "unknown_option": 1}
	}`), &props))
	m, err := Request(nil, map[string]interface{}{"properties": props})
	require.NoError(t, err)

	msg, ok := m.GetProperty("msg")
	require.True(t, ok)
	assert.Equal(t, "standard", msg.Analyzer)
	assert.Equal(t, "simple", msg.SearchAnalyzer)
	assert.True(t, msg.Highlightable)
	assert.True(t, msg.Store)
	raw, ok := m.GetProperty("msg.raw")
	require.True(t, ok)
	assert.Equal(t, "keyword", raw.Type)
	assert.Contains(t, msg.Fields, "raw")

	ts, ok := m.GetProperty("ts")
	require.True(t, ok)
	assert.Equal(t, "2006-01-02", ts.Format)
	assert.Equal(t, "+08:00", ts.TimeZone)
	assert.False(t, ts.Index)
	assert.True(t, ts.Store)
	assert.False(t, ts.Sortable)
	assert.False(t, ts.Aggregatable)
}

func TestRequest_Errors(t *testing.T) {
	tests := []struct {
		name  string
		props interface{}
		want  string
	}{
		{"properties not object", 1, "properties should be an object"},
		{"property not object", map[string]interface{}{"f": 1}, "properties [f] should be an object"},
		{"sub properties not object", map[string]interface{}{"f": map[string]interface{}{"properties": 1}}, "properties [f] should be an object"},
		{"sub properties error", map[string]interface{}{"f": map[string]interface{}{"properties": map[string]interface{}{"g": 1}}}, "properties [g] should be an object"},
		{"fields not object", map[string]interface{}{"f": map[string]interface{}{"type": "text", "fields": 1}}, "property.fields [f] should be an object"},
		{"sub field without type", map[string]interface{}{"f": map[string]interface{}{"type": "text", "fields": map[string]interface{}{"raw": map[string]interface{}{}}}}, "properties [type] should be exists"},
		{"sub field type not string", map[string]interface{}{"f": map[string]interface{}{"type": "text", "fields": map[string]interface{}{"raw": map[string]interface{}{"type": 1}}}}, "properties [type] should be a string"},
		{"sub field not marshalable", map[string]interface{}{"f": map[string]interface{}{"type": "text", "fields": map[string]interface{}{"raw": func() {}}}}, ""},
		{"sub field not a property", map[string]interface{}{"f": map[string]interface{}{"type": "text", "fields": map[string]interface{}{"raw": "keyword"}}}, ""},
		{"missing type", map[string]interface{}{"f": map[string]interface{}{}}, "properties [type] should be exists"},
		{"type not string", map[string]interface{}{"f": map[string]interface{}{"type": 1}}, "properties [type] should be a string"},
		{"unsupported type", map[string]interface{}{"f": map[string]interface{}{"type": "unknown"}}, "doesn't support type [unknown]"},
		{"bad time_zone", map[string]interface{}{"f": map[string]interface{}{"type": "date", "time_zone": "Mars/Base"}}, "time_zone parse err"},
		{"unknown analyzer", map[string]interface{}{"f": map[string]interface{}{"type": "text", "analyzer": "nope"}}, "nope"},
		{"unknown search_analyzer", map[string]interface{}{"f": map[string]interface{}{"type": "text", "search_analyzer": "nope"}}, "nope"},
		{"join empty parent", map[string]interface{}{"f": map[string]interface{}{"type": "join", "relations": map[string]interface{}{"": "a"}}}, "relations name should be non-empty"},
		{"join non string child", map[string]interface{}{"f": map[string]interface{}{"type": "join", "relations": map[string]interface{}{"q": []interface{}{1.0}}}}, "should be string or array of strings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Request(nil, map[string]interface{}{"properties": tt.props})
			assert.Nil(t, m)
			assert.Error(t, err)
			if tt.want != "" {
				assert.ErrorContains(t, err, tt.want)
			}
		})
	}
}

func TestRequest_SQLInvalid(t *testing.T) {
	enabled := config.Global.EnableSQLMappings
	t.Cleanup(func() { config.Global.EnableSQLMappings = enabled })
	config.Global.EnableSQLMappings = true

	_, err := Request(nil, map[string]interface{}{"sql": "DROP TABLE t"})
	assert.ErrorContains(t, err, "[mappings]")
}

func TestConvertToField(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		for _, v := range []map[string]interface{}{nil, {}} {
			got, err := convertToField(v)
			assert.NoError(t, err)
			assert.Empty(t, got)
		}
	})

	t.Run("properties", func(t *testing.T) {
		got, err := convertToField(map[string]interface{}{"raw": map[string]interface{}{"type": "keyword", "store": true}})
		require.NoError(t, err)
		assert.Equal(t, map[string]meta.Property{"raw": {Type: "keyword", Store: true}}, got)
	})

	t.Run("errors", func(t *testing.T) {
		for name, v := range map[string]map[string]interface{}{
			"marshal":   {"raw": make(chan int)},
			"unmarshal": {"raw": "keyword"},
		} {
			t.Run(name, func(t *testing.T) {
				got, err := convertToField(v)
				assert.Error(t, err)
				assert.Nil(t, got)
			})
		}
	})
}
