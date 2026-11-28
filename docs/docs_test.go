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
		OperationID string   `json:"operationId"`
		Produces    []string `json:"produces"`
		Responses   map[string]struct {
			Schema struct {
				Ref        string                 `json:"$ref"`
				Properties map[string]interface{} `json:"properties"`
			} `json:"schema"`
		} `json:"responses"`
	} `json:"paths"`
	Definitions map[string]struct {
		Required   []string               `json:"required"`
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

	for _, path := range []string{"/es/_query_rules/{id}", "/es/_query_rules/{id}/_rule/{rule_id}"} {
		assert.Equal(t, "#/definitions/rules.ResultResponse", s.Paths[path]["put"].Responses["201"].Schema.Ref, path)
	}
	assert.Contains(t, s.Paths["/es/_query_rules/{id}/_test"]["post"].Responses, "400")
	assert.Equal(t, []string{"match_criteria"}, s.Definitions["rules.TestRequest"].Required)
	assert.Contains(t, s.Paths["/es/_sql"]["post"].Produces, "text/csv")
	assert.Contains(t, s.Paths["/es/_synonyms/{id}"]["get"].Responses, "400")
	assert.Contains(t, s.Paths["/es/_msearch"]["post"].Responses["200"].Schema.Properties, "responses")
	sig := s.Definitions["meta.AggregationSignificantTerms"].Properties
	assert.NotContains(t, sig, "filter_duplicate_text")
	assert.NotContains(t, sig, "source_fields")
	assert.NotContains(t, s.Paths, "/api/fleet/enroll", "docs must not include untracked work")

	assert.Contains(t, s.Paths["/es/_ml/trained_models"], "get")
	for _, method := range []string{"get", "put", "delete"} {
		assert.Contains(t, s.Paths["/es/_ml/trained_models/{model_id}"], method)
	}
	assert.Equal(t, "#/definitions/meta.LTRModel", s.Paths["/es/_ml/trained_models/{model_id}"]["put"].Responses["200"].Schema.Ref)
	assert.Contains(t, s.Definitions["ltr.ListResponse"].Properties, "trained_model_configs")
	assert.Equal(t, "GetTrainedModelsByModelId", s.Paths["/es/_ml/trained_models/{model_id}"]["get"].OperationID)

	for path, id := range map[string]string{
		"/es/_alias":         "GetESAliases",
		"/es/{index}/_alias": "GetESAliasesByIndex",
		"/es/_alias/{alias}": "GetESAliasesByAlias",
	} {
		assert.Equal(t, id, s.Paths[path]["get"].OperationID, path)
	}
	assert.NotContains(t, s.Paths, "/es/{target}/_alias/{target_alias}", "not a registered route")
	assert.Equal(t, "GetDataStream", s.Paths["/es/_data_stream/{name}"]["get"].OperationID)
	assert.Equal(t, "PutDataStream", s.Paths["/es/_data_stream/{name}"]["put"].OperationID)
	assert.Contains(t, s.Definitions["meta.Shards"].Properties, "failures")

	for path, methods := range map[string][]string{
		"/api/cluster/remotes":                      {"get"},
		"/api/cluster/remotes/{alias}":              {"put", "delete"},
		"/es/_remote/info":                          {"get"},
		"/es/_cluster/settings":                     {"put"},
		"/es/_plugins/_replication/_autofollow":     {"get", "post", "delete"},
		"/es/_plugins/_replication/{index}/_start":  {"put"},
		"/es/_plugins/_replication/{index}/_pause":  {"post"},
		"/es/_plugins/_replication/{index}/_resume": {"post"},
		"/es/_plugins/_replication/{index}/_stop":   {"post"},
		"/es/_plugins/_replication/{index}/_status": {"get"},
		"/api/_ccr/indexes":                         {"get"},
		"/api/{target}/_ccr/changes":                {"post"},
		"/api/{target}/_ccr/snapshot":               {"post"},
		"/es/_inference":                            {"get"},
		"/es/_inference/{task_type}/{inference_id}": {"put", "post", "delete"},
	} {
		for _, method := range methods {
			assert.Contains(t, s.Paths[path], method, path)
		}
	}
	assert.Equal(t, "#/definitions/replication.ChangesResponse", s.Paths["/api/{target}/_ccr/changes"]["post"].Responses["200"].Schema.Ref)
	assert.Contains(t, s.Definitions["replication.Op"].Properties, "entry")

	ids := map[string]string{}
	for path, ops := range s.Paths {
		for method, op := range ops {
			if prev, ok := ids[op.OperationID]; ok {
				t.Errorf("operationId %s of %s %s already used by %s", op.OperationID, method, path, prev)
			}
			ids[op.OperationID] = method + " " + path
		}
	}
}
