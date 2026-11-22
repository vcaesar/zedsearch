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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestList(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		index, err := core.NewIndex("TestIndexList.index_1", "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)

		err = core.StoreIndex(index)
		assert.NoError(t, err)
	})

	t.Run("list", func(t *testing.T) {
		sortBy := []string{"name", "doc_num", "shard_num", "storage_size", "storage_type", "wal_size"}
		descArr := []string{"false", "true"}
		for _, s := range sortBy {
			for _, d := range descArr {

				c, w := utils.NewContext()
				params := map[string]string{
					"page_num":  "1",
					"page_size": "20",
					"sort_by":   s,
					"desc":      d,
					"name":      "TestIndexList.index_1",
				}
				utils.SetRequestURL(c, "/api/index", params)
				List(c)
				assert.Equal(t, http.StatusOK, w.Code)
				assert.NotNil(t, w.Body)

				resp := struct {
					List []*meta.Index `json:"list"`
					Page meta.Page     `json:"page"`
				}{}
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				assert.NoError(t, err)
				assert.NotNil(t, resp.List)
				assert.NotNil(t, resp.Page)
				assert.Equal(t, len(resp.List), 1)
				assert.Equal(t, resp.List[0].Name, "TestIndexList.index_1")
				assert.Equal(t, resp.Page.PageSize, int64(20))
				assert.Equal(t, resp.Page.PageNum, int64(1))
				assert.Equal(t, resp.Page.Total, int64(1))
				assert.Equal(t, len(resp.List), 1)
			}
		}
	})

	t.Run("cleanup", func(t *testing.T) {
		err := core.DeleteIndex("TestIndexList.index_1")
		assert.NoError(t, err)
	})
}

func TestIndexNameList(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		index, err := core.NewIndex("TestIndexNameList.index_1", "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)

		err = core.StoreIndex(index)
		assert.NoError(t, err)
	})

	t.Run("indexNameList", func(t *testing.T) {
		c, w := utils.NewContext()
		params := map[string]string{
			"name": "TestIndexNameList.index_1",
		}
		utils.SetRequestURL(c, "/api/index_name", params)
		IndexNameList(c)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotNil(t, w.Body)
		var resp []string
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, len(resp), 1)
		assert.Equal(t, resp[0], "TestIndexNameList.index_1")
		assert.Contains(t, w.Body.String(), "TestIndexNameList.index_1")
	})

	t.Run("cleanup", func(t *testing.T) {
		err := core.DeleteIndex("TestIndexNameList.index_1")
		assert.NoError(t, err)
	})
}

func TestList_Sort(t *testing.T) {
	prefix := "TestList_Sort."
	index1, index2 := prefix+"index_1", prefix+"index_2"
	for name, shards := range map[string]int64{index1: 1, index2: 2} {
		index, err := core.NewIndex(name, "disk", shards)
		assert.NoError(t, err)
		assert.NoError(t, core.StoreIndex(index))
	}
	t.Cleanup(func() {
		assert.NoError(t, core.DeleteIndex(index1))
		assert.NoError(t, core.DeleteIndex(index2))
	})

	tests := []struct {
		sortBy string
		desc   string
		want   []string
		strict bool
	}{
		{sortBy: "name", desc: "false", want: []string{index1, index2}, strict: true},
		{sortBy: "name", desc: "true", want: []string{index2, index1}, strict: true},
		{sortBy: "shard_num", desc: "false", want: []string{index1, index2}, strict: true},
		{sortBy: "shard_num", desc: "true", want: []string{index2, index1}, strict: true},
		{sortBy: "doc_num", desc: "false", want: []string{index1, index2}},
		{sortBy: "doc_num", desc: "true", want: []string{index1, index2}},
		{sortBy: "storage_size", desc: "false", want: []string{index1, index2}},
		{sortBy: "storage_size", desc: "true", want: []string{index1, index2}},
		{sortBy: "storage_type", desc: "false", want: []string{index1, index2}},
		{sortBy: "storage_type", desc: "true", want: []string{index1, index2}},
		{sortBy: "wal_size", desc: "false", want: []string{index1, index2}},
		{sortBy: "wal_size", desc: "true", want: []string{index1, index2}},
	}
	for _, tt := range tests {
		t.Run(tt.sortBy+"_desc_"+tt.desc, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestURL(c, "/api/index", map[string]string{"sort_by": tt.sortBy, "desc": tt.desc, "name": prefix})
			List(c)
			assert.Equal(t, http.StatusOK, w.Code)

			resp := struct {
				List []*meta.Index `json:"list"`
				Page meta.Page     `json:"page"`
			}{}
			assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, int64(2), resp.Page.Total)
			names := make([]string, 0, len(resp.List))
			for _, item := range resp.List {
				names = append(names, item.Name)
			}
			if tt.strict {
				assert.Equal(t, tt.want, names)
			} else {
				assert.ElementsMatch(t, tt.want, names)
			}
		})
	}

	t.Run("no match", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestURL(c, "/api/index", map[string]string{"name": prefix + "index_missing"})
		List(c)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, `{"list":[],"page":{"page_num":1,"page_size":0,"total":0}}`, w.Body.String())
	})
}

func TestIndexNameList_Limit(t *testing.T) {
	prefix := "TestIndexNameList_Limit.index_"
	names := make([]string, 0, 31)
	for i := 0; i < 31; i++ {
		name := fmt.Sprintf("%s%02d", prefix, i)
		index, err := core.NewIndex(name, "disk", 1)
		assert.NoError(t, err)
		assert.NoError(t, core.StoreIndex(index))
		names = append(names, name)
	}
	t.Cleanup(func() {
		for _, name := range names {
			assert.NoError(t, core.DeleteIndex(name))
		}
	})

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "case insensitive name", query: strings.ToUpper(prefix), want: names[:30]},
		{name: "all", query: "", want: core.ZED_INDEX_LIST.ListName()[:30]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestURL(c, "/api/index_name", map[string]string{"name": tt.query})
			IndexNameList(c)
			assert.Equal(t, http.StatusOK, w.Code)
			var resp []string
			assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, tt.want, resp)
		})
	}
}
