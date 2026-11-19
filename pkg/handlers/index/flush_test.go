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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestFlush(t *testing.T) {
	type args struct {
		code   int
		params map[string]string
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
				params: map[string]string{"target": "TestFlush.index_1"},
				result: "ok",
			},
			wantErr: false,
		},
		{
			name: "empty",
			args: args{
				code:   http.StatusBadRequest,
				params: map[string]string{"target": ""},
				result: "does not exists",
			},
			wantErr: false,
		},
	}

	index, err := core.NewIndex("TestFlush.index_1", "disk", 2)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex("TestFlush.index_1")) })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestParams(c, tt.args.params)
			Flush(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)

			resp := make(map[string]string)
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			assert.NoError(t, err)
		})
	}
}

// Writers and WAL reopen on the next request after a flush.
func TestFlushThenWrite(t *testing.T) {
	index, err := core.NewIndex("TestFlushThenWrite.index_1", "disk", 2)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex("TestFlushThenWrite.index_1")) })
	for _, id := range []string{"1", "2", "3"} {
		assert.NoError(t, index.CreateDocument(id, map[string]interface{}{"name": "zed"}, false))
	}
	hits := func() int {
		res, err := index.Search(&meta.ZedQuery{Size: 10})
		if err != nil {
			return -1
		}
		return res.Hits.Total.Value
	}

	c, w := utils.NewContext()
	utils.SetRequestParams(c, map[string]string{"target": "TestFlushThenWrite.index_1"})
	Flush(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 3, hits(), "flush applies the WAL")

	assert.NoError(t, index.CreateDocument("4", map[string]interface{}{"name": "zed"}, false))
	assert.Eventually(t, func() bool { return hits() == 4 }, 10*time.Second, 100*time.Millisecond)
}
