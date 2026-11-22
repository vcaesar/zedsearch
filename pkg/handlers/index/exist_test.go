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

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestExists(t *testing.T) {
	indexName := "TestExists.index_1"
	prepareIndex(t, indexName, "disk")
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(indexName)) })

	tests := []struct {
		name   string
		target string
		code   int
		result string
	}{
		{name: "exists", target: indexName, code: http.StatusOK, result: `{"message":"ok"}`},
		{name: "not exists", target: "TestExists.index_missing", code: http.StatusNotFound, result: ""},
		{name: "empty", target: "", code: http.StatusNotFound, result: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestParams(c, map[string]string{"target": tt.target})
			Exists(c)
			assert.Equal(t, tt.code, w.Code)
			assert.Equal(t, tt.result, w.Body.String())
		})
	}
}
