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

package logs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestCheckWindow(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	assert.NoError(t, CheckWindow(now.Add(-100*time.Hour), now, 0, 0), "disabled")
	assert.NoError(t, CheckWindow(now.Add(-time.Hour), now, 2*time.Hour, time.Hour))
	assert.NoError(t, CheckWindow(now.Add(time.Hour), now, 2*time.Hour, time.Hour), "bound is inclusive")
	assert.ErrorContains(t, CheckWindow(now.Add(-3*time.Hour), now, 2*time.Hour, 0), "older")
	assert.ErrorContains(t, CheckWindow(now.Add(2*time.Hour), now, 0, time.Hour), "future")
}

func TestParseJSON(t *testing.T) {
	docs, err := ParseJSON([]byte(` [{"message":"a"},{"level":"error","n":1}] `))
	require.NoError(t, err)
	assert.Equal(t, []map[string]interface{}{{"message": "a"}, {"level": "error", "n": float64(1)}}, docs)

	docs, err = ParseJSON([]byte(`{"message":"one"}`))
	require.NoError(t, err)
	assert.Equal(t, []map[string]interface{}{{"message": "one"}}, docs)

	for _, body := range []string{``, `"x"`, `[1]`, `[null]`, `{bad`, `[{"a":1}`} {
		_, err := ParseJSON([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestParseHEC(t *testing.T) {
	body := `{"time":1426279439.123,"host":"web-1","source":"app","sourcetype":"json","index":"ignored","event":"hello","fields":{"env":"prod"}}
{"time":"1426279440","event":{"message":"structured","level":"warn"}}{"event":{"k":"v"}}`
	docs, err := ParseHEC([]byte(body))
	require.NoError(t, err)
	require.Len(t, docs, 3)
	assert.Equal(t, map[string]interface{}{
		"message": "hello", "host.name": "web-1", "source": "app", "sourcetype": "json", "env": "prod",
		meta.TimeFieldName: "2015-03-13T20:43:59.123Z",
	}, docs[0])
	assert.Equal(t, map[string]interface{}{
		"message": "structured", "level": "warn", meta.TimeFieldName: "2015-03-13T20:44:00Z",
	}, docs[1])
	assert.Equal(t, map[string]interface{}{"k": "v"}, docs[2])

	for _, body := range []string{``, `{"time":1}`, `{"event":1}`, `{"event":"x","time":"abc"}`, `{"event":`, `[]`,
		`{"event":"x","time":1e300}`, `{"event":"x","time":253402300800}`, `{"event":"x","time":-62167219201}`} {
		_, err := ParseHEC([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestParseLoki(t *testing.T) {
	body := `{"streams":[{"stream":{"job":"api","level":"info"},"values":[
		["1704067200123456789","first line"],
		["1704067201000000000","second line",{"trace_id":"abc"}]]}]}`
	docs, err := ParseLoki([]byte(body))
	require.NoError(t, err)
	require.Len(t, docs, 2)
	assert.Equal(t, map[string]interface{}{
		"job": "api", "level": "info", "message": "first line",
		meta.TimeFieldName: "2024-01-01T00:00:00.123456789Z",
	}, docs[0])
	assert.Equal(t, "abc", docs[1]["trace_id"])
	assert.Equal(t, "2024-01-01T00:00:01Z", docs[1][meta.TimeFieldName])

	for _, body := range []string{
		`{}`, `bad`,
		`{"streams":[{"stream":{},"values":[["1"]]}]}`,
		`{"streams":[{"stream":{},"values":[[1,"x"]]}]}`,
		`{"streams":[{"stream":{},"values":[["abc","x"]]}]}`,
		`{"streams":[{"stream":{},"values":[["1",2]]}]}`,
		`{"streams":[{"stream":{},"values":[["1","x",{"a":1}]]}]}`,
	} {
		_, err := ParseLoki([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestParseOTLP(t *testing.T) {
	body := `{"resourceLogs":[{"resource":{"attributes":[
		{"key":"service.name","value":{"stringValue":"checkout"}},
		{"key":"host.name","value":{"stringValue":"node-1"}}]},
	"scopeLogs":[{"scope":{"name":"lib","version":"1.0"},"logRecords":[
		{"timeUnixNano":"1704067200123456789","severityNumber":17,"severityText":"ERROR",
		 "body":{"stringValue":"payment failed"},
		 "traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174",
		 "attributes":[{"key":"http.status","value":{"intValue":"502"}},
		   {"key":"retry","value":{"boolValue":true}},
		   {"key":"ratio","value":{"doubleValue":0.5}},
		   {"key":"host.name","value":{"stringValue":"override"}},
		   {"key":"tags","value":{"arrayValue":{"values":[{"stringValue":"a"},{"intValue":2}]}}}]},
		{"observedTimeUnixNano":1704067201000000000,"severityNumber":10,
		 "body":{"kvlistValue":{"values":[{"key":"k","value":{"stringValue":"v"}}]}},"eventName":"login"}
	]}]}]}`
	docs, err := ParseOTLP([]byte(body))
	require.NoError(t, err)
	require.Len(t, docs, 2)
	assert.Equal(t, map[string]interface{}{
		"service.name": "checkout", "host.name": "override",
		"scope.name": "lib", "scope.version": "1.0",
		meta.TimeFieldName: "2024-01-01T00:00:00.123456789Z",
		"level":            "ERROR", "severity_number": 17, "message": "payment failed",
		"trace_id": "5b8efff798038103d269b633813fc60c", "span_id": "eee19b7ec3c1b174",
		"http.status": int64(502), "retry": true, "ratio": 0.5,
		"tags": []interface{}{"a", int64(2)},
	}, docs[0])
	assert.Equal(t, "2024-01-01T00:00:01Z", docs[1][meta.TimeFieldName])
	assert.Equal(t, "info", docs[1]["level"], "derived from severityNumber")
	assert.Equal(t, map[string]interface{}{"k": "v"}, docs[1]["body"])
	assert.Equal(t, "login", docs[1]["event_name"])
	assert.Equal(t, "node-1", docs[1]["host.name"])
	assert.NotContains(t, docs[1], "message")

	docs, err = ParseOTLP([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{}]}]}]}`))
	require.NoError(t, err)
	assert.Equal(t, []map[string]interface{}{{}}, docs, "no time means ingestion time")

	for _, body := range []string{
		`bad`,
		`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"x1"}]}]}]}`,
		`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"attributes":[{"key":"a","value":{"intValue":"1.5"}}]}]}]}]}`,
		`{"resourceLogs":[{"resource":{"attributes":[{"key":"a","value":{"intValue":"z"}}]},"scopeLogs":[]}]}`,
	} {
		_, err := ParseOTLP([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestSeverityText(t *testing.T) {
	for number, want := range map[int]string{0: "", 1: "trace", 5: "debug", 9: "info", 12: "info", 13: "warn", 17: "error", 21: "fatal", 24: "fatal", 25: ""} {
		assert.Equal(t, want, SeverityText(number), number)
	}
}
