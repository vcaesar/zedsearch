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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

type spec struct {
	Paths map[string]map[string]struct {
		OperationID string   `json:"operationId"`
		Produces    []string `json:"produces"`
		Parameters  []struct {
			Name string `json:"name"`
			In   string `json:"in"`
			Type string `json:"type"`
		} `json:"parameters"`
		Responses map[string]struct {
			Schema struct {
				Type       string                 `json:"type"`
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

	for _, field := range []string{"knn", "retriever", "search_pipeline"} {
		assert.Contains(t, s.Definitions["meta.ZedQueryForSDK"].Properties, field)
	}
	assert.Contains(t, fmt.Sprint(s.Definitions["cluster.RemoteCluster"].Properties["password"]), "write only")

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
	for path, ops := range map[string]map[string]string{
		"/api/fleet/agents":        {"get": "ListFleetAgents"},
		"/api/fleet/agents/{id}":   {"delete": "DeleteFleetAgent"},
		"/api/fleet/policies":      {"get": "ListFleetPolicies", "post": "CreateFleetPolicy", "put": "CreateUpdateFleetPolicy"},
		"/api/fleet/policies/{id}": {"delete": "DeleteFleetPolicy"},
		"/api/fleet/enroll":        {"post": "FleetEnroll"},
		"/api/fleet/checkin/{id}":  {"post": "FleetCheckin"},
	} {
		for method, id := range ops {
			t.Run(method+" "+path, func(t *testing.T) {
				assert.Equal(t, id, s.Paths[path][method].OperationID)
			})
		}
	}
	assert.Equal(t, "#/definitions/fleetapi.EnrollResponse", s.Paths["/api/fleet/enroll"]["post"].Responses["200"].Schema.Ref)
	assert.Contains(t, s.Definitions["fleetapi.Policy"].Properties, "inputs")
	assert.Contains(t, s.Definitions["fleetapi.Input"].Properties, "paths")

	assert.Contains(t, s.Paths["/es/_ml/trained_models"], "get")
	for _, method := range []string{"get", "put", "delete"} {
		t.Run(method+" /es/_ml/trained_models/{model_id}", func(t *testing.T) {
			assert.Contains(t, s.Paths["/es/_ml/trained_models/{model_id}"], method)
		})
	}
	assert.Equal(t, "#/definitions/meta.LTRModel", s.Paths["/es/_ml/trained_models/{model_id}"]["put"].Responses["200"].Schema.Ref)
	assert.Contains(t, s.Definitions["ltr.ListResponse"].Properties, "trained_model_configs")
	assert.Equal(t, "GetTrainedModelsByModelId", s.Paths["/es/_ml/trained_models/{model_id}"]["get"].OperationID)

	for path, id := range map[string]string{
		"/es/_alias":         "GetESAliases",
		"/es/{index}/_alias": "GetESAliasesByIndex",
		"/es/_alias/{alias}": "GetESAliasesByAlias",
	} {
		t.Run("get "+path, func(t *testing.T) {
			assert.Equal(t, id, s.Paths[path]["get"].OperationID)
		})
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
			t.Run(method+" "+path, func(t *testing.T) {
				assert.Contains(t, s.Paths[path], method)
			})
		}
	}
	snapshot := s.Paths["/api/{target}/_ccr/snapshot"]["post"].Responses
	assert.Equal(t, "file", snapshot["200"].Schema.Type, "tar archive body")
	assert.Equal(t, errRef, snapshot["404"].Schema.Ref)
	assert.Equal(t, "#/definitions/replication.ChangesResponse", s.Paths["/api/{target}/_ccr/changes"]["post"].Responses["200"].Schema.Ref)
	assert.Contains(t, s.Definitions["replication.Op"].Properties, "entry")

	for path, ops := range map[string]map[string]string{
		"/es/":                     {"get": "ESInfo"},
		"/es/_license":             {"get": "ESLicense"},
		"/es/_xpack":               {"get": "ESXPack"},
		"/es/_search":              {"post": "SearchAll"},
		"/es/_count":               {"post": "CountAll"},
		"/es/{index}/_msearch":     {"post": "MSearchIndex"},
		"/es/{index}/_bulk":        {"post": "ESBulkIndex"},
		"/es/{index}/_refresh":     {"post": "ESRefresh"},
		"/es/{index}/_doc":         {"post": "ESIndexDocument"},
		"/es/{index}/_doc/{id}":    {"get": "ESGetDocument", "put": "ESIndexDocumentWithID", "delete": "ESDeleteDocument"},
		"/es/{index}/_create/{id}": {"put": "ESCreateDocument"},
		"/es/{index}/_update/{id}": {"post": "ESUpdateDocument"},
	} {
		for method, id := range ops {
			t.Run(method+" "+path, func(t *testing.T) {
				assert.Equal(t, id, s.Paths[path][method].OperationID)
			})
		}
	}
	assert.Equal(t, "#/definitions/meta.HTTPResponseID", s.Paths["/es/{index}/_doc"]["post"].Responses["200"].Schema.Ref)
	assert.Equal(t, "#/definitions/elastic.ESInfo", s.Paths["/es/"]["get"].Responses["200"].Schema.Ref)
	for _, path := range []string{"/es/_search", "/es/{index}/_search"} {
		t.Run("allow_partial_search_results "+path, func(t *testing.T) {
			var in, typ string
			for _, p := range s.Paths[path]["post"].Parameters {
				if p.Name == "allow_partial_search_results" {
					in, typ = p.In, p.Type
				}
			}
			assert.Equal(t, "query", in)
			assert.Equal(t, "boolean", typ)
		})
	}
	var settingsBody bool
	for _, p := range s.Paths["/es/_cluster/settings"]["put"].Parameters {
		settingsBody = settingsBody || p.In == "body"
	}
	assert.True(t, settingsBody, "PUT /es/_cluster/settings declares its body")

	ids := map[string]string{}
	for path, ops := range s.Paths {
		for method, op := range ops {
			if op.OperationID == "" {
				continue
			}
			if prev, ok := ids[op.OperationID]; ok {
				t.Errorf("operationId %s of %s %s already used by %s", op.OperationID, method, path, prev)
			}
			ids[op.OperationID] = method + " " + path
		}
	}
}
