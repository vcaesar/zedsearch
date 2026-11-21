/* Copyright 2022 Zinc Labs Inc. and Contributors
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
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestMapping(t *testing.T) {
	t.Run("create index", func(t *testing.T) {
		index, err := core.NewIndex("TestMapping.index_1", "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)

		err = core.StoreIndex(index)
		assert.NoError(t, err)
	})

	t.Run("set mapping", func(t *testing.T) {
		type args struct {
			code    int
			data    map[string]interface{}
			rawData string
			target  string
			result  string
		}
		tests := []struct {
			name    string
			args    args
			wantErr bool
		}{
			{
				name: "normal",
				args: args{
					code: http.StatusOK,
					data: map[string]interface{}{
						"properties": map[string]interface{}{
							"Athlete": map[string]interface{}{
								"type":          "text",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  false,
								"highlightable": false,
							},
							"City": map[string]interface{}{
								"type":          "keyword",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
							"Gender": map[string]interface{}{
								"type":          "bool",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
							"time": map[string]interface{}{
								"type":          "time",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
						},
					},
					target: "TestMapping.index_1",
					result: `{"message":"ok"}`,
				},
				wantErr: false,
			},
			{
				name: "with not exists index",
				args: args{
					code: http.StatusOK,
					data: map[string]interface{}{
						"properties": map[string]interface{}{
							"Athlete": map[string]interface{}{
								"type":          "text",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  false,
								"highlightable": false,
							},
						},
					},
					target: "TestMapping.index_2",
					result: `{"message":"ok"}`,
				},
				wantErr: true,
			},
			{
				name: "empty_body",
				args: args{
					code:   http.StatusOK,
					data:   map[string]interface{}{},
					target: "TestMapping.index_3",
					result: `{"message":"ok"}`,
				},
				wantErr: false,
			},
			{
				name: "empty",
				args: args{
					code:   http.StatusBadRequest,
					data:   map[string]interface{}{},
					target: "",
					result: `{"error":"index.name should be not empty"}`,
				},
				wantErr: false,
			},
			{
				name: "with error json",
				args: args{
					code:    http.StatusBadRequest,
					rawData: `{"x":y}`,
					target:  "TestMapping.index_4",
					result:  `{"error":"invalid character 'y' looking for beginning of value"}`,
				},
				wantErr: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c, w := utils.NewContext()
				if tt.args.data != nil {
					utils.SetRequestData(c, tt.args.data)
				}
				if tt.args.rawData != "" {
					utils.SetRequestData(c, tt.args.rawData)
				}
				utils.SetRequestParams(c, map[string]string{"target": tt.args.target})
				SetMapping(c)
				assert.Equal(t, tt.args.code, w.Code)
				assert.Equal(t, tt.args.result, w.Body.String())
			})
		}
	})

	t.Run("get mapping", func(t *testing.T) {
		type args struct {
			code   int
			target string
			result string
		}
		tests := []struct {
			name    string
			args    args
			wantErr bool
		}{
			{
				name: "normal",
				args: args{
					code:   http.StatusOK,
					target: "TestMapping.index_1",
					result: `{"mappings":{"properties`,
				},
				wantErr: false,
			},
			{
				name: "empty",
				args: args{
					code:   http.StatusBadRequest,
					target: "",
					result: `does not exists`,
				},
				wantErr: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c, w := utils.NewContext()
				utils.SetRequestParams(c, map[string]string{"target": tt.args.target})
				GetMapping(c)
				assert.Equal(t, tt.args.code, w.Code)
				assert.Contains(t, w.Body.String(), tt.args.result)
			})
		}
	})

	t.Run("delete index", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			_ = core.DeleteIndex(fmt.Sprintf("TestMapping.index_%d", i))
		}
	})
}

// An update the index rejects leaves its mappings as they were.
func TestSetMapping_RejectedUpdateKeepsMappings(t *testing.T) {
	name := "TestSetMapping_RejectedUpdateKeepsMappings.index_1"
	index, err := core.NewIndex(name, "disk", 1)
	require.NoError(t, err)
	mappings := meta.NewMappings()
	mappings.SetSourceEnabled(false)
	mappings.SetProperty("title", meta.NewProperty("text"))
	require.NoError(t, index.SetMappings(mappings))
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })

	put := func(data map[string]interface{}) (int, string) {
		c, w := utils.NewContext()
		utils.SetRequestData(c, data)
		utils.SetRequestParams(c, map[string]string{"target": name})
		SetMapping(c)
		return w.Code, w.Body.String()
	}
	nested := map[string]interface{}{"properties": map[string]interface{}{"items": map[string]interface{}{"type": "nested"}}}
	for _, attempt := range []string{"first", "retry"} {
		t.Run(attempt, func(t *testing.T) {
			code, body := put(nested)
			assert.Equal(t, http.StatusBadRequest, code)
			assert.Contains(t, body, "requires _source", "not the duplicate field error of a half applied update")
			_, ok := index.GetMappings().GetProperty("items")
			assert.False(t, ok)
		})
	}
	t.Run("valid update applies", func(t *testing.T) {
		code, body := put(map[string]interface{}{"properties": map[string]interface{}{"tag": map[string]interface{}{"type": "keyword"}}})
		assert.Equal(t, http.StatusOK, code, body)
		_, ok := index.GetMappings().GetProperty("tag")
		assert.True(t, ok)
		_, ok = index.GetMappings().GetProperty("title")
		assert.True(t, ok)
	})
}

func TestSetMapping_Branches(t *testing.T) {
	prefix := "TestSetMapping_Branches.index_"
	t.Cleanup(func() {
		for i := 1; i <= 3; i++ {
			if _, exists := core.GetIndex(fmt.Sprintf("%s%d", prefix, i)); exists {
				assert.NoError(t, core.DeleteIndex(fmt.Sprintf("%s%d", prefix, i)))
			}
		}
	})

	subFields := `{"_source":{"enabled":true},"properties":{"title":{"type":"text","fields":{"raw":{"type":"keyword"}}}}}`
	ensureIndex2 := func(t *testing.T) {
		if _, exists := core.GetIndex(prefix + "2"); exists {
			return
		}
		c, w := utils.NewContext()
		utils.SetRequestData(c, subFields)
		utils.SetRequestParams(c, map[string]string{"target": prefix + "2"})
		SetMapping(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	tests := []struct {
		name   string
		setup  func(t *testing.T)
		target string
		data   string
		code   int
		result string
	}{
		{name: "invalid mapping", target: prefix + "1", data: `{"properties":{"f":{"type":"TestSetMapping_Branches"}}}`, code: http.StatusBadRequest, result: `doesn't support type [testsetmapping_branches]`},
		{name: "invalid index name", target: "_" + prefix + "1", data: `{"properties":{"f":{"type":"keyword"}}}`, code: http.StatusInternalServerError, result: `index name cannot start with _`},
		{name: "create with sub fields", target: prefix + "2", data: subFields, code: http.StatusOK, result: `{"message":"ok"}`},
		{name: "change _source", setup: ensureIndex2, target: prefix + "2", data: `{"_source":{"enabled":false}}`, code: http.StatusBadRequest, result: `cannot change [_source] of an existing index`},
		{name: "existing field", setup: ensureIndex2, target: prefix + "2", data: `{"properties":{"title":{"type":"text"}}}`, code: http.StatusBadRequest, result: `already exists mapping of field [title]`},
		{name: "nested without _source", target: prefix + "3", data: `{"_source":{"enabled":false},"properties":{"items":{"type":"nested"}}}`, code: http.StatusBadRequest, result: `nested field [items] requires _source`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t)
			}
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.data)
			utils.SetRequestParams(c, map[string]string{"target": tt.target})
			SetMapping(c)
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.result)
		})
	}

	t.Run("change _all", func(t *testing.T) {
		ensureIndex2(t)
		index, exists := core.GetIndex(prefix + "2")
		require.True(t, exists)
		c, w := utils.NewContext()
		utils.SetRequestData(c, map[string]interface{}{"_all": map[string]interface{}{"enabled": !index.GetMappings().AllEnabled()}})
		utils.SetRequestParams(c, map[string]string{"target": prefix + "2"})
		SetMapping(c)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), `cannot change [_all] of an existing index`)
	})

	t.Run("state", func(t *testing.T) {
		ensureIndex2(t)
		index, exists := core.GetIndex(prefix + "2")
		require.True(t, exists)
		mappings := index.GetMappings()
		assert.True(t, mappings.SourceEnabled())
		title, ok := mappings.GetProperty("title")
		require.True(t, ok)
		assert.Equal(t, "text", title.Type)
		assert.Equal(t, "keyword", title.Fields["raw"].Type)
		assert.Empty(t, title.Fields["raw"].Fields)
	})
}

func TestSetMapping_NormalizesSubFields(t *testing.T) {
	name := "TestSetMapping_NormalizesSubFields.index_1"
	index, err := core.NewIndex(name, "disk", 1)
	require.NoError(t, err)
	title := meta.NewProperty("text")
	title.Fields["raw"] = meta.NewProperty("keyword")
	mappings := meta.NewMappings()
	mappings.SetProperty("title", title)
	require.NoError(t, index.SetMappings(mappings))
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })
	require.NotNil(t, index.GetMappings().Properties["title"].Fields["raw"].Fields)

	c, w := utils.NewContext()
	utils.SetRequestData(c, map[string]interface{}{"properties": map[string]interface{}{"tag": map[string]interface{}{"type": "keyword"}}})
	utils.SetRequestParams(c, map[string]string{"target": name})
	SetMapping(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"message":"ok"}`, w.Body.String())

	got := index.GetMappings()
	_, ok := got.GetProperty("tag")
	assert.True(t, ok)
	raw := got.Properties["title"].Fields["raw"]
	assert.Equal(t, "keyword", raw.Type)
	assert.Nil(t, raw.Fields, "sub fields can't have sub fields")
}
