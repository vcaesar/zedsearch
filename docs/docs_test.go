/* Copyright 2026 AtomAI
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

package docs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

type spec struct {
	Paths map[string]map[string]struct {
		Responses map[string]struct {
			Schema struct {
				Ref string `json:"$ref"`
			} `json:"schema"`
		} `json:"responses"`
	} `json:"paths"`
	Definitions map[string]struct {
		Properties map[string]interface{} `json:"properties"`
	} `json:"definitions"`
}

func TestSwaggerSpec(t *testing.T) {
	var s spec
	require.NoError(t, json.Unmarshal([]byte(SwaggerInfo.ReadDoc()), &s))

	assert.Contains(t, s.Definitions["meta.ZedQueryForSDK"].Properties, "knn")

	const errRef = "#/definitions/meta.HTTPResponseError"
	tests := []struct {
		path   string
		status []string
	}{
		{"/api/{index}/_json", []string{"500", "503"}},
		{"/api/{index}/loki/api/v1/push", []string{"500", "503"}},
		{"/api/{index}/v1/logs", []string{"415", "500", "503"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			responses := s.Paths[tt.path]["post"].Responses
			for _, code := range tt.status {
				assert.Equal(t, errRef, responses[code].Schema.Ref, code)
			}
		})
	}
	assert.Contains(t, s.Paths["/api/{index}/_hec"]["post"].Responses, "503")
	assert.NotContains(t, s.Paths, "/api/fleet/enroll", "docs must not include untracked work")
}
