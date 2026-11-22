/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI
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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestCreate(t *testing.T) {
	type args struct {
		code       int
		data       string
		params     map[string]string
		result     string
		mappingRes string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "create by json",
			args: args{
				code:   http.StatusOK,
				data:   `{"name":"TestIndexCreate.index_1","disk":"disk"}`,
				params: map[string]string{"target": ""},
				result: `"message":"ok"`,
			},
			wantErr: false,
		},
		{
			name: "create by target",
			args: args{
				code:   http.StatusOK,
				data:   `{"name":"","disk":"disk"}`,
				params: map[string]string{"target": "TestIndexCreate.index_2"},
				result: `"message":"ok"`,
			},
			wantErr: false,
		},
		{
			name: "create by error json",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"name":"TestIndexCreate.index_3"x,"disk":"disk"}`,
				params: map[string]string{"target": ""},
				result: `"error":`,
			},
			wantErr: true,
		},
		{
			name: "create by empty",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"name":"","disk":"disk"}`,
				params: map[string]string{"target": ""},
				result: "should be not empty",
			},
			wantErr: true,
		},
		{
			name: "create with analyzer",
			args: args{
				code:   http.StatusOK,
				data:   `{"name":"TestIndexCreate.index_5","disk":"disk","settings":{"analysis":{"analyzer":{"test_analyzer":{"type":"custom","tokenizer":"standard","filter":["lowercase"]}}}}}`,
				params: map[string]string{"target": ""},
				result: `"message":"ok"`,
			},
			wantErr: false,
		},
		{
			name: "create with sub-fields",
			args: args{
				code:       http.StatusOK,
				data:       `{"name":"TestIndexCreate.index_6","disk":"disk","mappings":{"properties":{"@timestamp":{"type":"date"},"Athlete":{"type":"text","fields":{"my_keyword":{"type":"keyword"}}}}}}`,
				mappingRes: `"Athlete.my_keyword":{"type":"keyword"`,
				params:     map[string]string{"target": ""},
				result:     `"message":"ok"`,
			},
			wantErr: false,
		},
		{
			name: "create with unconfigured s3 storage",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"name":"TestIndexCreate.index_7","storage_type":"s3"}`,
				params: map[string]string{"target": ""},
				result: "ZED_S3_BUCKET",
			},
			wantErr: true,
		},
		{
			name: "create with unknown storage",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"name":"TestIndexCreate.index_8","storage_type":"floppy"}`,
				params: map[string]string{"target": ""},
				result: "storage_type [floppy] is invalid",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			utils.SetRequestParams(c, tt.args.params)
			Create(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)

			resp := make(map[string]string)
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			assert.NoError(t, err)

			if tt.args.mappingRes != "" {
				c, w := utils.NewContext()
				utils.SetRequestParams(c, map[string]string{"target": resp["index"]})

				GetMapping(c)
				assert.Equal(t, tt.args.code, w.Code)
				assert.Contains(t, w.Body.String(), tt.args.mappingRes)
			}

			if !tt.wantErr {
				err = core.DeleteIndex(resp["index"])
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateES(t *testing.T) {
	type args struct {
		code   int
		data   string
		params map[string]string
		result string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "create by json",
			args: args{
				code:   http.StatusOK,
				data:   `{"name":"TestIndexCreate.index_1"}`,
				params: map[string]string{"target": ""},
				result: `"acknowledged":true`,
			},
			wantErr: false,
		},
		{
			name: "create by target",
			args: args{
				code:   http.StatusOK,
				data:   `{"name":""}`,
				params: map[string]string{"target": "TestIndexCreate.index_2"},
				result: `"acknowledged":true`,
			},
			wantErr: false,
		},
		{
			name: "create by target",
			args: args{
				code:   http.StatusOK,
				data:   `{"name":""}`,
				params: map[string]string{"target": "TestIndexCreate.index_3"},
				result: `"acknowledged":true`,
			},
			wantErr: false,
		},
		{
			name: "create by error json",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"name":"TestIndexCreate.index_4"x}`,
				params: map[string]string{"target": ""},
				result: `"error":`,
			},
			wantErr: true,
		},
		{
			name: "create by empty",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"name":"","disk":"disk"}`,
				params: map[string]string{"target": ""},
				result: "should be not empty",
			},
			wantErr: true,
		},
		{
			name: "create with analyzer",
			args: args{
				code:   http.StatusOK,
				data:   `{"name":"TestIndexCreate.index_6","disk":"disk","settings":{"analysis":{"analyzer":{"test_analyzer":{"type":"custom","tokenizer":"standard","filter":["lowercase"]}}}}}`,
				params: map[string]string{"target": ""},
				result: `"acknowledged":true`,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			utils.SetRequestParams(c, tt.args.params)
			CreateES(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)

			resp := make(map[string]interface{})
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			assert.NoError(t, err)

			if !tt.wantErr {
				err = core.DeleteIndex(resp["index"].(string))
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateIndexWorker(t *testing.T) {
	existing := "TestCreateIndexWorker.index_1"
	prepareIndex(t, existing, "disk")
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(existing)) })

	tests := []struct {
		name      string
		index     *meta.IndexSimple
		wantErr   string
		wantShard int64
	}{
		{
			name:    "already exists",
			index:   &meta.IndexSimple{Name: existing},
			wantErr: "index [" + existing + "] already exists",
		},
		{
			name: "invalid analyzer",
			index: &meta.IndexSimple{Name: "TestCreateIndexWorker.index_2", Settings: &meta.IndexSettings{
				Analysis: &meta.IndexAnalysis{Analyzer: map[string]*meta.Analyzer{"broken": {}}},
			}},
			wantErr: "[analyzer] [broken] is missing tokenizer",
		},
		{
			name: "invalid mappings",
			index: &meta.IndexSimple{Name: "TestCreateIndexWorker.index_3", Mappings: map[string]interface{}{
				"properties": map[string]interface{}{"field": map[string]interface{}{"type": "TestCreateIndexWorker.type"}},
			}},
			wantErr: "[mappings] properties [field] doesn't support type [testcreateindexworker.type]",
		},
		{
			name:    "invalid name",
			index:   &meta.IndexSimple{Name: "_TestCreateIndexWorker.index_4"},
			wantErr: "index name cannot start with _",
		},
		{
			name: "missing synonyms set",
			index: &meta.IndexSimple{Name: "TestCreateIndexWorker.index_5", Settings: &meta.IndexSettings{
				Analysis: &meta.IndexAnalysis{TokenFilter: map[string]interface{}{
					"syn": map[string]interface{}{"type": "synonym", "synonyms_set": "TestCreateIndexWorker.missing"},
				}},
			}},
			wantErr: "synonyms set [TestCreateIndexWorker.missing] not found",
		},
		{
			name:      "shards from settings",
			index:     &meta.IndexSimple{Name: "TestCreateIndexWorker.index_6", Settings: &meta.IndexSettings{NumberOfShards: 3}},
			wantShard: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CreateIndexWorker(tt.index, "")
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				if tt.index.Name != existing {
					_, exists := core.GetIndex(tt.index.Name)
					assert.False(t, exists)
				}
				return
			}
			assert.NoError(t, err)
			index, exists := core.GetIndex(tt.index.Name)
			assert.True(t, exists)
			assert.Equal(t, tt.wantShard, index.GetShardNum())
			assert.NoError(t, core.DeleteIndex(tt.index.Name))
		})
	}
}
