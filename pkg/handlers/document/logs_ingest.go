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
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/ider"
	"github.com/vcaesar/zedsearch/pkg/logs"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

// LogsJSON ingests an OpenObserve `_json` body, a JSON array of log records.
//
// @Id LogsJSON
// @Summary Ingest log records (OpenObserve _json)
// @security BasicAuth
// @Tags    Document
// @Accept  json
// @Produce json
// @Param   index  path  string  true  "Index"
// @Param   logs   body  []map[string]interface{}  true  "Log records"
// @Success 200 {object} logs.IngestResponse
// @Failure 400 {object} meta.HTTPResponseError
// @Router /api/{index}/_json [post]
func LogsJSON(c zhttp.Context) {
	docs, ok := parseLogsBody(c, logs.ParseJSON)
	if !ok {
		return
	}
	status, err := IngestLogs(c.Param("target"), docs)
	if err != nil {
		c.JSON(ingestErrorStatus(err), meta.HTTPResponseError{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, logs.IngestResponse{Code: http.StatusOK, Status: []logs.StreamStatus{status}})
}

// LogsHEC ingests Splunk HTTP Event Collector events.
//
// @Id LogsHEC
// @Summary Ingest Splunk HEC events
// @security BasicAuth
// @Tags    Document
// @Accept  plain
// @Produce json
// @Param   index   path  string  true  "Index"
// @Param   events  body  string  true  "Concatenated HEC event objects"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /api/{index}/_hec [post]
func LogsHEC(c zhttp.Context) {
	body, err := readLogsBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, zhttp.H{"text": err.Error(), "code": 6})
		return
	}
	docs, err := logs.ParseHEC(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, zhttp.H{"text": "Invalid data format: " + err.Error(), "code": 6})
		return
	}
	status, err := IngestLogs(c.Param("target"), docs)
	if err != nil {
		c.JSON(ingestErrorStatus(err), zhttp.H{"text": err.Error(), "code": 8})
		return
	}
	if status.Failed > 0 {
		c.JSON(http.StatusBadRequest, zhttp.H{"text": status.Error, "code": 6, "successful": status.Successful, "failed": status.Failed})
		return
	}
	c.JSON(http.StatusOK, zhttp.H{"text": "Success", "code": 0})
}

// LokiPush ingests a Loki push API JSON request.
//
// @Id LokiPush
// @Summary Ingest Loki push request (JSON)
// @security BasicAuth
// @Tags    Document
// @Accept  json
// @Produce json
// @Param   index  path  string  true  "Index"
// @Param   push   body  map[string]interface{}  true  "Loki push request"
// @Success 204
// @Failure 400 {object} logs.IngestResponse
// @Router /api/{index}/loki/api/v1/push [post]
func LokiPush(c zhttp.Context) {
	docs, ok := parseLogsBody(c, logs.ParseLoki)
	if !ok {
		return
	}
	status, err := IngestLogs(c.Param("target"), docs)
	if err != nil {
		c.JSON(ingestErrorStatus(err), meta.HTTPResponseError{Error: err.Error()})
		return
	}
	if status.Failed > 0 {
		c.JSON(http.StatusBadRequest, logs.IngestResponse{Code: http.StatusBadRequest, Status: []logs.StreamStatus{status}})
		return
	}
	c.Status(http.StatusNoContent)
}

// OTLPLogs ingests an OTLP/HTTP JSON logs export request.
//
// @Id OTLPLogs
// @Summary Ingest OTLP logs (HTTP/JSON)
// @security BasicAuth
// @Tags    Document
// @Accept  json
// @Produce json
// @Param   index  path  string  true  "Index"
// @Param   logs   body  map[string]interface{}  true  "OTLP logs export request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} meta.HTTPResponseError
// @Router /api/{index}/v1/logs [post]
func OTLPLogs(c zhttp.Context) {
	if ct := c.GetHeader("Content-Type"); ct != "" && !isJSONMediaType(ct) {
		c.JSON(http.StatusUnsupportedMediaType, meta.HTTPResponseError{Error: "only OTLP/HTTP JSON is supported"})
		return
	}
	docs, ok := parseLogsBody(c, logs.ParseOTLP)
	if !ok {
		return
	}
	status, err := IngestLogs(c.Param("target"), docs)
	if err != nil {
		c.JSON(ingestErrorStatus(err), meta.HTTPResponseError{Error: err.Error()})
		return
	}
	if status.Failed > 0 {
		c.JSON(http.StatusOK, zhttp.H{"partialSuccess": zhttp.H{"rejectedLogRecords": status.Failed, "errorMessage": status.Error}})
		return
	}
	c.JSON(http.StatusOK, zhttp.H{})
}

// isJSONMediaType reports whether ct is application/json, parameters and
// case aside.
func isJSONMediaType(ct string) bool {
	mediaType, _, err := mime.ParseMediaType(ct)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

// ingestErrorStatus answers 503 for a storage failure so log shippers retry
// the batch, 500 otherwise.
func ingestErrorStatus(err error) int {
	var storageErr *core.StorageError
	if errors.As(err, &storageErr) {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// IngestLogs writes records to target, creating it as a logs index when it
// doesn't exist and no template sets its index_type. Invalid records, including
// those outside the configured ingestion window, are counted as failed and
// don't stop the rest. A storage failure stops the batch and is returned: the
// client retries it whole, so delivery is at least once and records written
// before the failure may be stored twice.
func IngestLogs(target string, docs []map[string]interface{}) (logs.StreamStatus, error) {
	status := logs.StreamStatus{Name: target}
	index, err := getOrCreateLogsIndex(target)
	if err != nil {
		return status, err
	}
	upto, future := config.Global.Logs.IngestAllowedUpto, config.Global.Logs.IngestAllowedInFuture
	for _, doc := range docs {
		err := checkLogWindow(index, doc, upto, future)
		if err == nil {
			err = index.CreateDocument(ider.Generate(), doc, false)
		}
		var storageErr *core.StorageError
		if errors.As(err, &storageErr) {
			return status, err
		}
		if err != nil {
			status.Failed++
			if status.Error == "" {
				status.Error = err.Error()
			}
			continue
		}
		status.Successful++
	}
	return status, nil
}

func checkLogWindow(index *core.Index, doc map[string]interface{}, upto, future time.Duration) error {
	if upto == 0 && future == 0 {
		return nil
	}
	now := time.Now()
	_, ts, err := logs.Normalize(doc, index.GetMappings(), now)
	if err != nil {
		return err
	}
	return logs.CheckWindow(ts, now, upto, future)
}

// getOrCreateLogsIndex types a new index as logs before it is stored and
// published, unless a template already set its index_type.
func getOrCreateLogsIndex(name string) (*core.Index, error) {
	if name == "" {
		return nil, fmt.Errorf("index is required")
	}
	index, _, err := core.GetOrCreateIndexWith(name, "", 0, func(index *core.Index) error {
		if settings := index.GetSettings(); settings != nil && settings.IndexType != "" {
			return nil
		}
		return index.SetSettings(&meta.IndexSettings{IndexType: meta.IndexTypeLogs})
	})
	return index, err
}

func parseLogsBody(c zhttp.Context, parse func([]byte) ([]map[string]interface{}, error)) ([]map[string]interface{}, bool) {
	if c.Param("target") == "" {
		c.JSON(http.StatusBadRequest, meta.HTTPResponseError{Error: "target is empty"})
		return nil, false
	}
	body, err := readLogsBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, meta.HTTPResponseError{Error: err.Error()})
		return nil, false
	}
	docs, err := parse(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, meta.HTTPResponseError{Error: err.Error()})
		return nil, false
	}
	return docs, true
}

// readLogsBody reads the request body, decompressing gzip as log shippers
// usually send it. The decompressed size is capped by zed_server_body_limit.
func readLogsBody(c zhttp.Context) ([]byte, error) {
	var r io.Reader = c.Body()
	if strings.EqualFold(c.GetHeader("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("invalid gzip body: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	limit := int64(config.Global.ServerBodyLimit)
	if limit <= 0 {
		return io.ReadAll(r)
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("request body exceeds %d bytes", limit)
	}
	return data, nil
}
