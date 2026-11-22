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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestGet(t *testing.T) {
	indexName := "TestGet.index_1"
	prepareIndex(t, indexName, "disk")
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(indexName)) })

	t.Run("exists", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestParams(c, map[string]string{"target": indexName})
		Get(c)
		require.Equal(t, http.StatusOK, w.Code)

		resp := new(meta.Index)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		assert.Equal(t, indexName, resp.Name)
		assert.Equal(t, "disk", resp.StorageType)
		assert.Equal(t, int64(2), resp.ShardNum)
	})

	tests := []struct {
		name   string
		target string
		result string
	}{
		{name: "not exists", target: "TestGet.index_missing", result: `{"error":"index TestGet.index_missing does not exists"}`},
		{name: "empty", target: "", result: `{"error":"index  does not exists"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestParams(c, map[string]string{"target": tt.target})
			Get(c)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, tt.result, w.Body.String())
		})
	}
}
