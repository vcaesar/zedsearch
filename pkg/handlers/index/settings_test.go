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
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestSettings(t *testing.T) {
	t.Run("create index", func(t *testing.T) {
		index, err := core.NewIndex("TestSettings.index_1", "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)

		err = core.StoreIndex(index)
		assert.NoError(t, err)
	})

	t.Run("set settings", func(t *testing.T) {
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
						"settings": map[string]interface{}{
							"number_of_shards":   3,
							"number_of_replicas": 1,
						},
					},
					target: "TestSettings.index_1",
					result: `{"message":"ok"}`,
				},
				wantErr: false,
			},
			{
				name: "with not exists index",
				args: args{
					code: http.StatusOK,
					data: map[string]interface{}{
						"settings": map[string]interface{}{
							"number_of_shards":   3,
							"number_of_replicas": 1,
							"analysis": map[string]interface{}{
								"analyzer": map[string]interface{}{
									"default": map[string]interface{}{
										"type": "standard",
									},
								},
							},
						},
					},
					target: "TestSettings.index_2",
					result: `{"message":"ok"}`,
				},
				wantErr: true,
			},
			{
				name: "empty",
				args: args{
					code:   http.StatusOK,
					data:   map[string]interface{}{},
					target: "TestSettings.index_3",
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
					target:  "TestSettings.index_4",
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
				SetSettings(c)
				assert.Equal(t, tt.args.code, w.Code)
				assert.Equal(t, tt.args.result, w.Body.String())
			})
		}
	})

	t.Run("get settings", func(t *testing.T) {
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
					target: "TestSettings.index_1",
					result: `{"settings":`,
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
				GetSettings(c)
				assert.Equal(t, tt.args.code, w.Code)
				assert.Contains(t, w.Body.String(), tt.args.result)
			})
		}
	})

	t.Run("delete index", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			_ = core.DeleteIndex(fmt.Sprintf("TestSettings.index_%d", i))
		}
	})
}

func TestSetSettings_Branches(t *testing.T) {
	prefix := "TestSetSettings_Branches.index_"
	t.Cleanup(func() {
		for i := 1; i <= 5; i++ {
			if _, exists := core.GetIndex(fmt.Sprintf("%s%d", prefix, i)); exists {
				assert.NoError(t, core.DeleteIndex(fmt.Sprintf("%s%d", prefix, i)))
			}
		}
	})

	tests := []struct {
		name   string
		target string
		data   string
		code   int
		result string
	}{
		{name: "null settings", target: prefix + "1", data: `null`, code: http.StatusOK, result: `{"message":"ok"}`},
		{name: "invalid analyzer", target: prefix + "2", data: `{"analysis":{"analyzer":{"broken":{}}}}`, code: http.StatusBadRequest, result: `[analyzer] [broken] is missing tokenizer`},
		{name: "invalid index name", target: "_" + prefix + "1", data: `{"number_of_shards":2}`, code: http.StatusInternalServerError, result: `index name cannot start with _`},
		{name: "create with shards", target: prefix + "3", data: `{"number_of_shards":3}`, code: http.StatusOK, result: `{"message":"ok"}`},
		{name: "update replicas", target: prefix + "3", data: `{"number_of_replicas":2}`, code: http.StatusOK, result: `{"message":"ok"}`},
		{name: "update analyzer", target: prefix + "3", data: `{"analysis":{"analyzer":{"std":{"tokenizer":"standard"}}}}`, code: http.StatusBadRequest, result: `can't update analyzer for existing index`},
		{name: "tier on creation", target: prefix + "4", data: `{"tier":"warm"}`, code: http.StatusBadRequest, result: `index tier can't be set to [warm] on creation`},
		{name: "missing synonyms set", target: prefix + "5", data: `{"analysis":{"token_filter":{"syn":{"type":"synonym","synonyms_set":"TestSetSettings_Branches.missing"}}}}`, code: http.StatusInternalServerError, result: `synonyms set [TestSetSettings_Branches.missing] not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.data)
			utils.SetRequestParams(c, map[string]string{"target": tt.target})
			SetSettings(c)
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.result)
		})
	}

	t.Run("state", func(t *testing.T) {
		_, exists := core.GetIndex(prefix + "1")
		assert.False(t, exists, "null settings must not create the index")
		index, exists := core.GetIndex(prefix + "3")
		assert.True(t, exists)
		assert.Equal(t, int64(3), index.GetShardNum())
		assert.Equal(t, int64(2), index.GetSettings().NumberOfReplicas)
		assert.Empty(t, index.GetSettings().Analysis)
	})
}
