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

package meta

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProperty_DeepClone(t *testing.T) {
	prop := NewProperty("text")
	prop.AddField("sub_field", NewProperty("text"))
	prop.Sortable = true
	prop.Aggregatable = true
	prop.Index = true
	prop.Store = true
	prop.Aggregatable = true
	prop.Highlightable = true
	prop.Analyzer = "some_nice_analyzer"
	prop.SearchAnalyzer = "search_analyzer"
	prop.Format = "2009-11-10T23:00:00Z"

	clone := prop.DeepClone()
	assert.Equal(t, prop, clone)
}

func TestMappings_RangeProperty(t *testing.T) {
	m := NewMappings()
	m.SetProperty("a", NewProperty("text"))
	m.SetProperty("b", NewProperty("keyword"))
	m.SetProperty("c", NewProperty("nested"))

	t.Run("visits all", func(t *testing.T) {
		got := map[string]string{}
		m.RangeProperty(func(field string, prop Property) bool {
			got[field] = prop.Type
			return true
		})
		assert.Equal(t, map[string]string{"a": "text", "b": "keyword", "c": "nested"}, got)
	})

	t.Run("stops early", func(t *testing.T) {
		n := 0
		m.RangeProperty(func(string, Property) bool {
			n++
			return false
		})
		assert.Equal(t, 1, n)
	})
}

func TestMappings_Source(t *testing.T) {
	m := NewMappings()
	assert.True(t, m.SourceEnabled())
	got, err := m.MarshalJSON()
	assert.NoError(t, err)
	assert.JSONEq(t, `{"properties":{}}`, string(got))

	m.SetSourceEnabled(false)
	assert.False(t, m.SourceEnabled())
	got, err = m.MarshalJSON()
	assert.NoError(t, err)
	assert.JSONEq(t, `{"properties":{},"_source":{"enabled":false}}`, string(got))

	clone := m.DeepClone()
	assert.NotSame(t, m.Source, clone.Source)
	assert.False(t, clone.SourceEnabled())
	clone.SetSourceEnabled(true)
	assert.False(t, m.SourceEnabled(), "clone shares no state")
}

func TestMappings_All(t *testing.T) {
	m := NewMappings()
	assert.False(t, m.AllEnabled(), "follows zed_all_field, off by default")
	m.SetAllEnabled(true)
	assert.True(t, m.AllEnabled())
	got, err := m.MarshalJSON()
	assert.NoError(t, err)
	assert.JSONEq(t, `{"properties":{},"_all":{"enabled":true}}`, string(got))

	clone := m.DeepClone()
	assert.NotSame(t, m.All, clone.All)
	assert.True(t, clone.AllEnabled())
	clone.SetAllEnabled(false)
	assert.True(t, m.AllEnabled(), "clone shares no state")
}
