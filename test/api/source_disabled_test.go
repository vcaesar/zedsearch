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

package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestApiSourceDisabled(t *testing.T) {
	const index = "TestApiSourceDisabled.index_1"
	defer request("DELETE", "/api/index/"+index, nil)

	t.Run("create with _source disabled", func(t *testing.T) {
		body := bytes.NewBufferString(`{"mappings":{"_source":{"enabled":false},"properties":{"body":{"type":"text"}}}}`)
		resp := request("PUT", "/es/"+index, body)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

		resp = request("GET", "/es/"+index+"/_mapping", nil)
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.Contains(t, resp.Body.String(), `"_source":{"enabled":false}`)
	})

	t.Run("search returns no _source", func(t *testing.T) {
		resp := request("PUT", "/es/"+index+"/_doc/1", bytes.NewBufferString(`{"body":"quick brown fox"}`))
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		require.Eventually(t, func() bool {
			resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(`{"query":{"match":{"body":"brown"}}}`))
			return resp.Code == http.StatusOK && strings.Contains(resp.Body.String(), `"_id":"1"`)
		}, 10*time.Second, 50*time.Millisecond, "document not indexed")

		resp = request("POST", "/es/"+index+"/_search", bytes.NewBufferString(`{"query":{"match":{"body":"brown"}}}`))
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		var result struct {
			Hits struct {
				Hits []map[string]interface{} `json:"hits"`
			} `json:"hits"`
		}
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &result))
		require.Len(t, result.Hits.Hits, 1)
		hit := result.Hits.Hits[0]
		assert.Equal(t, "1", hit["_id"])
		assert.NotContains(t, hit, "_source")
	})

	t.Run("cannot change _source", func(t *testing.T) {
		resp := request("PUT", "/es/"+index+"/_mapping", bytes.NewBufferString(`{"_source":{"enabled":true}}`))
		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.Contains(t, resp.Body.String(), "cannot change [_source]")
	})

	t.Run("reindex is rejected", func(t *testing.T) {
		body := bytes.NewBufferString(`{"source":{"index":"` + index + `"},"dest":{"index":"` + index + `_copy"}}`)
		resp := request("POST", "/es/_reindex", body)
		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.Contains(t, resp.Body.String(), "_source disabled")
	})
}
