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

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

// newDocShard returns the shard of an index that is not stored, using
// mappings as they are.
func newDocShard(t *testing.T, name string, mappings *meta.Mappings) *IndexShard {
	t.Helper()
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	index.ref.Mappings = mappings
	return anyShard(index)
}

func TestIndexShard_BuildBlugeDocumentFromJSONCases(t *testing.T) {
	m := meta.NewMappings()
	m.SetProperty("comments", meta.Property{Type: "nested", Index: true})
	m.SetProperty("my_join", meta.Property{Type: "join", Index: true})
	m.SetProperty("vec", meta.Property{Type: "vector", Index: true, Dims: 2})
	m.SetProperty("loc", meta.Property{Type: "geo_point", Index: true})
	m.SetProperty("title", meta.Property{Type: "text", Index: true, Fields: map[string]meta.Property{"geo": {Type: "geo_point", Index: true}}})
	m.SetProperty("title.geo", meta.Property{Type: "geo_point", Index: true})
	m.SetProperty("tag", meta.Property{Type: "keyword", Index: true})
	shard := newDocShard(t, "TestIndexShard_BuildBlugeDocumentFromJSONCases.index_1", m)

	tests := []struct {
		name    string
		doc     map[string]interface{}
		wantErr bool
	}{
		{name: "nested and join are not indexed", doc: map[string]interface{}{"comments": []interface{}{}, "my_join": "question"}},
		{name: "empty strings", doc: map[string]interface{}{"title": "", "tag": ""}},
		{name: "invalid vector", doc: map[string]interface{}{"vec": "bad"}, wantErr: true},
		{name: "invalid array element", doc: map[string]interface{}{"loc": []interface{}{true}}, wantErr: true},
		{name: "invalid sub field", doc: map[string]interface{}{"title": "abc"}, wantErr: true},
		{name: "invalid timestamp", doc: map[string]interface{}{meta.TimeFieldName: "yesterday"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := shard.BuildBlugeDocumentFromJSON("1", tt.doc)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}

	t.Run("vector field without a name", func(t *testing.T) {
		unnamed := meta.NewMappings()
		unnamed.SetProperty("", meta.Property{Type: "vector", Index: true})
		err := shard.buildField(unnamed, riot.NewDocument("1"), "", []interface{}{1.0, 2.0})
		assert.Error(t, err)
	})
}

func TestIndexShard_checkFieldInvalidValues(t *testing.T) {
	m := meta.NewMappings()
	shard := newDocShard(t, "TestIndexShard_checkFieldInvalidValues.index_1", m)
	for _, typ := range []string{"numeric", "bool"} {
		t.Run(typ, func(t *testing.T) {
			key := "field_" + typ
			m.SetProperty(key, meta.Property{Type: typ, Index: true})
			err := shard.checkField(m, map[string]interface{}{}, key, map[string]interface{}{}, 0, false)
			assert.Error(t, err)
		})
	}
}

func TestIndexShard_CheckDocumentSkipsHiddenFields(t *testing.T) {
	m := meta.NewMappings()
	m.SetProperty(meta.NestedPathField, meta.NewProperty("keyword"))
	m.SetProperty("comments", meta.Property{Type: "nested", Index: true})
	m.SetProperty("j1", meta.Property{Type: "join", Index: true, Relations: map[string][]string{"q": {"a"}}})
	m.SetProperty("j2", meta.Property{Type: "join", Index: true})
	shard := newDocShard(t, "TestIndexShard_CheckDocumentSkipsHiddenFields.index_1", m)

	t.Run("hidden nested document", func(t *testing.T) {
		_, err := shard.CheckDocument("1", map[string]interface{}{meta.NestedPathField: "comments", "comments": "x"}, false, 0)
		assert.NoError(t, err)
	})

	t.Run("second join field", func(t *testing.T) {
		_, err := shard.CheckDocument("1", map[string]interface{}{"j2": "x"}, false, 0)
		assert.NoError(t, err)
	})
}

func TestIndexShard_CheckDocumentMappingsRejected(t *testing.T) {
	m := meta.NewMappings()
	m.SetSourceEnabled(false)
	m.SetProperty("comments", meta.Property{Type: "nested", Index: true})
	shard := newDocShard(t, "TestIndexShard_CheckDocumentMappingsRejected.index_1", m)
	_, err := shard.CheckDocument("1", map[string]interface{}{"count": 1.0}, false, 0)
	assert.Error(t, err, "a new field updates mappings that nested fields without _source reject")
}

func TestRewriteJoinFieldCases(t *testing.T) {
	m := meta.NewMappings()
	m.SetProperty("my_join", meta.Property{Type: "join", Index: true, Relations: map[string][]string{"question": {"answer"}}})
	tests := []struct {
		name    string
		doc     map[string]interface{}
		wantErr bool
	}{
		{name: "parent without name", doc: map[string]interface{}{"my_join.parent": "q1"}, wantErr: true},
		{name: "empty relation", doc: map[string]interface{}{"my_join": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rewriteJoinField(m, tt.doc)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}

}

func TestNestedElementHelpers(t *testing.T) {
	for _, suffix := range []string{"x", "-1", "x.name"} {
		_, ok := nestedElementIndex(suffix)
		assert.False(t, ok, suffix)
	}
	idx, ok := nestedElementIndex("2.name")
	assert.True(t, ok)
	assert.Equal(t, 2, idx)

	object := map[string]interface{}{"name": "a"}
	tests := []struct {
		name string
		doc  map[string]interface{}
		path string
		want []interface{}
	}{
		{name: "parent is not an object", doc: map[string]interface{}{"a": "x"}, path: "a.b"},
		{name: "missing", doc: map[string]interface{}{}, path: "a"},
		{name: "single object", doc: map[string]interface{}{"a": object}, path: "a", want: []interface{}{object}},
		{name: "scalar", doc: map[string]interface{}{"a": "x"}, path: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nestedElements(tt.doc, tt.path))
		})
	}
}
