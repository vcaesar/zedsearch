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

package document

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/logs"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func logsRequest(t *testing.T, target, body string) (*zhttp.HTTPContext, *bytes.Buffer) {
	t.Helper()
	c, w := utils.NewContext()
	utils.SetRequestData(c, body)
	utils.SetRequestParams(c, map[string]string{"target": target})
	return c, w.Body
}

func cleanupIndex(t *testing.T, name string) {
	t.Cleanup(func() {
		if _, ok := core.GetIndex(name); ok {
			assert.NoError(t, core.DeleteIndex(name))
		}
	})
}

func waitDocCount(t *testing.T, name string, want int64) {
	t.Helper()
	index, ok := core.GetIndex(name)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		return index.GetStats().DocNum == uint64(want)
	}, 10*time.Second, 50*time.Millisecond)
}

func TestLogsJSON(t *testing.T) {
	name := "TestLogsJSON.index_1"
	cleanupIndex(t, name)
	c, body := logsRequest(t, name, `[{"message":"a","level":"info"},{"@timestamp":"bad"},{"_timestamp":1704067201123456,"log":"b"}]`)
	LogsJSON(c)
	require.Equal(t, http.StatusOK, c.ResponseStatus(), body.String())
	var resp logs.IngestResponse
	require.NoError(t, json.Unmarshal(body.Bytes(), &resp))
	require.Len(t, resp.Status, 1)
	assert.Equal(t, name, resp.Status[0].Name)
	assert.Equal(t, 2, resp.Status[0].Successful)
	assert.Equal(t, 1, resp.Status[0].Failed)
	assert.Contains(t, resp.Status[0].Error, "@timestamp")

	index, ok := core.GetIndex(name)
	require.True(t, ok)
	assert.Equal(t, meta.IndexTypeLogs, index.GetIndexType(), "missing index is created as logs")
	waitDocCount(t, name, 2)

	for _, tt := range []struct{ name, target, body string }{
		{"no target", "", `[]`},
		{"not json", name, `not json`},
		{"top-level null", name, `null`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := logsRequest(t, tt.target, tt.body)
			LogsJSON(c)
			assert.Equal(t, http.StatusBadRequest, c.ResponseStatus())
		})
	}
}

// A WAL or metadata failure is retryable: the batch stops and the client
// gets 503, invalid records stay rejected records.
func TestIngestLogsStorageFailure(t *testing.T) {
	name := "TestIngestLogsStorageFailure.index_1"
	cleanupIndex(t, name)
	_, err := getOrCreateLogsIndex(name)
	require.NoError(t, err)
	core.WriteGuard = func(string) error { return assert.AnError }
	t.Cleanup(func() { core.WriteGuard = nil })

	status, err := IngestLogs(name, []map[string]interface{}{{"@timestamp": "bad"}, {"message": "a"}, {"message": "b"}})
	var storageErr *core.StorageError
	require.ErrorAs(t, err, &storageErr)
	assert.Equal(t, 1, status.Failed, "the invalid record before the failure")
	assert.Equal(t, 0, status.Successful)

	for _, tt := range []struct {
		name   string
		handle func(zhttp.Context)
		body   string
	}{
		{"json", LogsJSON, `[{"message":"a"}]`},
		{"hec", LogsHEC, `{"event":"a"}`},
		{"loki", LokiPush, `{"streams":[{"stream":{},"values":[["1704067200000000000","a"]]}]}`},
		{"otlp", OTLPLogs, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"a"}}]}]}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, body := logsRequest(t, name, tt.body)
			tt.handle(c)
			assert.Equal(t, http.StatusServiceUnavailable, c.ResponseStatus(), body.String())
		})
	}
}

func TestIngestErrorStatus(t *testing.T) {
	assert.Equal(t, http.StatusServiceUnavailable, ingestErrorStatus(fmt.Errorf("wrap: %w", &core.StorageError{Err: assert.AnError})))
	assert.Equal(t, http.StatusInternalServerError, ingestErrorStatus(assert.AnError))
}

// Concurrent first requests for a new stream all get it as a logs index, none
// while it is created but not yet typed.
func TestGetOrCreateLogsIndexConcurrent(t *testing.T) {
	name := "TestGetOrCreateLogsIndexConcurrent.index_1"
	cleanupIndex(t, name)
	types := make([]string, 16)
	var wg sync.WaitGroup
	for i := range types {
		wg.Go(func() {
			index, err := getOrCreateLogsIndex(name)
			if assert.NoError(t, err) {
				types[i] = index.GetIndexType()
			}
		})
	}
	wg.Wait()
	for _, typ := range types {
		assert.Equal(t, meta.IndexTypeLogs, typ)
	}
}

func TestIngestLogsKeepsExistingStandardIndex(t *testing.T) {
	name := "TestIngestLogsStandard.index_1"
	cleanupIndex(t, name)
	_, _, err := core.GetOrCreateIndex(name, "", 0)
	require.NoError(t, err)
	status, err := IngestLogs(name, []map[string]interface{}{{"message": "x"}})
	require.NoError(t, err)
	assert.Equal(t, 1, status.Successful)
	index, _ := core.GetIndex(name)
	assert.Equal(t, meta.IndexTypeStandard, index.GetIndexType())
}

func TestIngestLogsWindow(t *testing.T) {
	name := "TestIngestLogsWindow.index_1"
	cleanupIndex(t, name)
	upto, future := config.Global.Logs.IngestAllowedUpto, config.Global.Logs.IngestAllowedInFuture
	config.Global.Logs.IngestAllowedUpto, config.Global.Logs.IngestAllowedInFuture = time.Hour, time.Minute
	t.Cleanup(func() {
		config.Global.Logs.IngestAllowedUpto, config.Global.Logs.IngestAllowedInFuture = upto, future
	})
	now := time.Now().UTC()
	status, err := IngestLogs(name, []map[string]interface{}{
		{"message": "now"},
		{"@timestamp": now.Add(-30 * time.Minute).Format(time.RFC3339Nano)},
		{"@timestamp": now.Add(-2 * time.Hour).Format(time.RFC3339Nano)},
		{"@timestamp": now.Add(time.Hour).Format(time.RFC3339Nano)},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, status.Successful)
	assert.Equal(t, 2, status.Failed)
	assert.Contains(t, status.Error, "older")

	_, err = IngestLogs("", nil)
	assert.Error(t, err)
}

func TestLogsHEC(t *testing.T) {
	name := "TestLogsHEC.index_1"
	cleanupIndex(t, name)
	c, body := logsRequest(t, name, `{"time":1704067200,"event":"hello","host":"h1"}{"event":{"message":"x"}}`)
	LogsHEC(c)
	require.Equal(t, http.StatusOK, c.ResponseStatus(), body.String())
	assert.JSONEq(t, `{"text":"Success","code":0}`, body.String())
	waitDocCount(t, name, 2)

	for _, tt := range []struct{ name, body, want string }{
		{"rejected record", `{"event":{"@timestamp":"bad"}}`, "@timestamp"},
		{"invalid event", `{"event":1}`, "Invalid data format"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, body := logsRequest(t, name, tt.body)
			LogsHEC(c)
			assert.Equal(t, http.StatusBadRequest, c.ResponseStatus())
			assert.Contains(t, body.String(), tt.want)
		})
	}
}

func TestLokiPush(t *testing.T) {
	name := "TestLokiPush.index_1"
	cleanupIndex(t, name)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, err := zw.Write([]byte(`{"streams":[{"stream":{"job":"api"},"values":[["1704067200000000000","line 1"],["1704067201000000000","line 2"]]}]}`))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	c, _ := logsRequest(t, name, "")
	c.Request.Body = io.NopCloser(&gz)
	c.Request.Header.Set("Content-Encoding", "gzip")
	LokiPush(c)
	require.Equal(t, http.StatusNoContent, c.ResponseStatus())
	waitDocCount(t, name, 2)

	for _, tt := range []struct{ name, body, encoding string }{
		{"invalid gzip", "plain text", "gzip"},
		{"no streams", `{"streams":[]}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := logsRequest(t, name, tt.body)
			c.Request.Header.Set("Content-Encoding", tt.encoding)
			LokiPush(c)
			assert.Equal(t, http.StatusBadRequest, c.ResponseStatus())
		})
	}
}

func TestOTLPLogs(t *testing.T) {
	name := "TestOTLPLogs.index_1"
	cleanupIndex(t, name)
	req := `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"api"}}]},
		"scopeLogs":[{"logRecords":[
			{"timeUnixNano":"1704067200000000000","severityNumber":9,"body":{"stringValue":"ok"}},
			{"attributes":[{"key":"@timestamp","value":{"stringValue":"bad"}}]}]}]}]}`
	c, body := logsRequest(t, name, req)
	OTLPLogs(c)
	require.Equal(t, http.StatusOK, c.ResponseStatus(), body.String())
	assert.Contains(t, body.String(), `"rejectedLogRecords":1`)
	waitDocCount(t, name, 1)

	for _, tt := range []struct {
		contentType string
		want        int
	}{
		{"", http.StatusOK},
		{"application/json", http.StatusOK},
		{"application/JSON", http.StatusOK},
		{"Application/Json; charset=utf-8", http.StatusOK},
		{"application/x-protobuf", http.StatusUnsupportedMediaType},
		{"application/jsonl", http.StatusUnsupportedMediaType},
		{"text/json-ish", http.StatusUnsupportedMediaType},
		{"application/json;;", http.StatusUnsupportedMediaType},
	} {
		t.Run("content type "+tt.contentType, func(t *testing.T) {
			c, body := logsRequest(t, name, `{}`)
			if tt.contentType != "" {
				c.Request.Header.Set("Content-Type", tt.contentType)
			}
			OTLPLogs(c)
			assert.Equal(t, tt.want, c.ResponseStatus(), body.String())
		})
	}
}

func TestReadLogsBodyLimit(t *testing.T) {
	limit := config.Global.ServerBodyLimit
	config.Global.ServerBodyLimit = 4
	t.Cleanup(func() { config.Global.ServerBodyLimit = limit })
	c, _ := logsRequest(t, "x", "12345")
	_, err := readLogsBody(c)
	assert.ErrorContains(t, err, "exceeds")
	c, _ = logsRequest(t, "x", "1234")
	data, err := readLogsBody(c)
	require.NoError(t, err)
	assert.Equal(t, "1234", string(data))
}
