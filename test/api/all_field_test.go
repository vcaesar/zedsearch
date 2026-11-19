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
)

func TestApiAllField(t *testing.T) {
	const index = "TestApiAllField.index_1"
	defer request("DELETE", "/api/index/"+index, nil)

	resp := request("PUT", "/es/"+index, bytes.NewBufferString(`{"mappings":{"_all":{"enabled":true},"properties":{"body":{"type":"text"}}}}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	resp = request("GET", "/es/"+index+"/_mapping", nil)
	assert.Contains(t, resp.Body.String(), `"_all":{"enabled":true}`)
	resp = request("GET", "/api/"+index+"/_mapping", nil)
	assert.Contains(t, resp.Body.String(), `"_all":{"enabled":true}`)

	resp = request("PUT", "/es/"+index+"/_mapping", bytes.NewBufferString(`{"_all":{"enabled":false}}`))
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "cannot change [_all]")

	resp = request("PUT", "/es/"+index+"/_doc/1", bytes.NewBufferString(`{"body":"quick brown fox"}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Eventually(t, func() bool {
		resp := request("POST", "/es/"+index+"/_search", bytes.NewBufferString(`{"query":{"match_phrase":{"_all":"brown fox"}}}`))
		return resp.Code == http.StatusOK && strings.Contains(resp.Body.String(), `"_id":"1"`)
	}, 10*time.Second, 50*time.Millisecond, "_all field not searchable")
}
