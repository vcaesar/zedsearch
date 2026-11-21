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

package search

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestSearchDSL(t *testing.T) {
	indexName := "TestSearchDSL.index_1"
	type args struct {
		code   int
		data   string
		params map[string]string
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "normal",
			args: args{
				code:   http.StatusOK,
				data:   `{"query":{"match_all":{}},"size":10}`,
				params: map[string]string{"target": indexName},
				result: "successful",
			},
		},
		{
			name: "multiple index",
			args: args{
				code:   http.StatusOK,
				data:   `{"query":{"match_all":{}},"size":10}`,
				result: "successful",
			},
		},
		{
			name: "index not found",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"query":{"match_all":{}},"size":10}`,
				params: map[string]string{"target": "NotExist" + indexName},
				result: "does not exists",
			},
		},
		{
			name: "query jsone error",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"query":{"match_all":{x}},"size":10}`,
				params: map[string]string{"target": indexName},
				result: "invalid character",
			},
		},
	}

	t.Run("prepare", func(t *testing.T) {
		index, err := core.NewIndex(indexName, "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)
		err = core.StoreIndex(index)
		assert.NoError(t, err)
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			utils.SetRequestParams(c, tt.args.params)
			SearchDSL(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}

	t.Run("cleanup", func(t *testing.T) {
		err := core.DeleteIndex(indexName)
		assert.NoError(t, err)
	})
}

func TestMultipleSearch(t *testing.T) {
	indexName := "TestMultipleSearch.index_1"
	type args struct {
		code   int
		data   string
		params map[string]string
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "normal",
			args: args{
				code: http.StatusOK,
				data: `{"index":"` + indexName + `"}
{"query":{"match_all":{}},"size":10}`,
				params: map[string]string{"target": indexName},
				result: "successful",
			},
		},
		{
			name: "multiple index",
			args: args{
				code: http.StatusOK,
				data: `{"index":["` + indexName + `"]}
{"query":{"match_all":{}},"size":10}`,
				result: "successful",
			},
		},
		{
			name: "not exists",
			args: args{
				code: http.StatusOK,
				data: `{"index":"TestMultipleSearch.notExists"}
{"query":{"match_all":{}},"size":10}`,
				result: "does not exists",
			},
		},
	}

	t.Run("prepare", func(t *testing.T) {
		index, err := core.NewIndex(indexName, "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)
		err = core.StoreIndex(index)
		assert.NoError(t, err)
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			utils.SetRequestParams(c, tt.args.params)
			MultipleSearch(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}

	t.Run("cleanup", func(t *testing.T) {
		err := core.DeleteIndex(indexName)
		assert.NoError(t, err)
	})
}

func TestMultipleSearchLines(t *testing.T) {
	indexName := "TestMultipleSearchLines.index_1"
	index, err := core.NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(indexName)) })
	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"tag": "msearchlines"}, false))
	require.Eventually(t, func() bool {
		res, err := index.Search(&meta.ZedQuery{Size: 1})
		return err == nil && res.Hits.Total.Value == 1
	}, 10*time.Second, 20*time.Millisecond, "document not indexed")

	query := `{"query":{"match":{"tag":"msearchlines"}}}`
	tests := []struct {
		name   string
		data   string
		params map[string]string
		errors []string
	}{
		{
			name:   "invalid query line",
			data:   `{"index":"` + indexName + `"}` + "\n" + `{"query":x}`,
			errors: []string{"invalid character"},
		},
		{
			name:   "invalid header searches all indexes",
			data:   "not json\n" + query,
			errors: []string{""},
		},
		{
			name:   "header without index uses target",
			data:   "{}\n" + query,
			params: map[string]string{"target": indexName},
			errors: []string{""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.data)
			utils.SetRequestParams(c, tt.params)
			MultipleSearch(c)
			require.Equal(t, http.StatusOK, w.Code)

			var out struct {
				Responses []meta.SearchResponse `json:"responses"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Responses, len(tt.errors))
			for i, want := range tt.errors {
				resp := out.Responses[i]
				if want != "" {
					assert.Contains(t, resp.Error, want)
					continue
				}
				assert.Empty(t, resp.Error)
				require.Len(t, resp.Hits.Hits, 1)
				assert.Equal(t, indexName, resp.Hits.Hits[0].Index)
			}
		})
	}
}
