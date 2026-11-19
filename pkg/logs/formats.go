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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package logs

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// Converters below turn OpenObserve-supported wire formats into log documents.
// Event time is always written to @timestamp as RFC3339Nano so both profiles
// parse it; the line text goes to message.

// StreamStatus mirrors an log ingestion status entry.
type StreamStatus struct {
	Name       string `json:"name"`
	Successful int    `json:"successful"`
	Failed     int    `json:"failed"`
	Error      string `json:"error,omitempty"`
}

// IngestResponse mirrors the log `_json`/`_multi` ingestion response.
type IngestResponse struct {
	Code   int            `json:"code"`
	Status []StreamStatus `json:"status"`
}

// ParseJSON decodes an log `_json` body: an array of objects, or one object.
func ParseJSON(body []byte) ([]map[string]interface{}, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '{' {
		var doc map[string]interface{}
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("invalid JSON object: %w", err)
		}
		return []map[string]interface{}{doc}, nil
	}
	var docs []map[string]interface{}
	if err := json.Unmarshal(body, &docs); err != nil {
		return nil, fmt.Errorf("body must be a JSON array of objects: %w", err)
	}
	if docs == nil { // top-level null
		return nil, fmt.Errorf("body must be a JSON array of objects")
	}
	for i, doc := range docs {
		if doc == nil {
			return nil, fmt.Errorf("record %d must be an object", i)
		}
	}
	return docs, nil
}

type hecEntry struct {
	Time       stdjson.Number         `json:"time"`
	Host       string                 `json:"host"`
	Source     string                 `json:"source"`
	SourceType string                 `json:"sourcetype"`
	Fields     map[string]interface{} `json:"fields"`
	Event      stdjson.RawMessage     `json:"event"`
}

// ParseHEC decodes Splunk HTTP Event Collector events, which may be
// concatenated or newline-delimited. The per-event `index` is ignored: events
// go to the index in the request path.
func ParseHEC(body []byte) ([]map[string]interface{}, error) {
	dec := stdjson.NewDecoder(bytes.NewReader(body))
	var docs []map[string]interface{}
	for {
		var e hecEntry
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid HEC event %d: %w", len(docs), err)
		}
		doc, err := hecDocument(e)
		if err != nil {
			return nil, fmt.Errorf("invalid HEC event %d: %w", len(docs), err)
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no HEC events")
	}
	return docs, nil
}

// hecTimeMin and hecTimeMax bound the epoch seconds RFC 3339 can write:
// years 0 to 9999.
const hecTimeMin, hecTimeMax = -62167219200, 253402300799

func hecDocument(e hecEntry) (map[string]interface{}, error) {
	var event interface{}
	if len(e.Event) == 0 {
		return nil, fmt.Errorf("event is required")
	}
	if err := json.Unmarshal(e.Event, &event); err != nil {
		return nil, err
	}
	doc := map[string]interface{}{}
	switch v := event.(type) {
	case string:
		doc["message"] = v
	case map[string]interface{}:
		doc = v
	default:
		return nil, fmt.Errorf("event must be a string or an object")
	}
	if e.Time != "" {
		sec, err := e.Time.Float64()
		if err != nil || sec < hecTimeMin || sec >= hecTimeMax {
			return nil, fmt.Errorf("invalid time [%s]", e.Time)
		}
		whole := math.Floor(sec)
		ts := time.Unix(int64(whole), int64(math.Round((sec-whole)*1e6))*1e3)
		doc[meta.TimeFieldName] = ts.UTC().Format(time.RFC3339Nano)
	}
	for k, v := range map[string]string{"host.name": e.Host, "source": e.Source, "sourcetype": e.SourceType} {
		if v != "" {
			doc[k] = v
		}
	}
	for k, v := range e.Fields {
		doc[k] = v
	}
	return doc, nil
}

type lokiPush struct {
	Streams []struct {
		Stream map[string]string      `json:"stream"`
		Values [][]stdjson.RawMessage `json:"values"`
	} `json:"streams"`
}

// ParseLoki decodes a Loki push API JSON body. Labels and structured metadata
// become fields; entries are [ "<unix ns>", "<line>", {metadata}? ].
func ParseLoki(body []byte) ([]map[string]interface{}, error) {
	var req lokiPush
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid Loki push request: %w", err)
	}
	if len(req.Streams) == 0 {
		return nil, fmt.Errorf("no Loki streams")
	}
	var docs []map[string]interface{}
	for i, s := range req.Streams {
		for j, entry := range s.Values {
			doc, err := lokiDocument(s.Stream, entry)
			if err != nil {
				return nil, fmt.Errorf("stream %d entry %d: %w", i, j, err)
			}
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

func lokiDocument(labels map[string]string, entry []stdjson.RawMessage) (map[string]interface{}, error) {
	if len(entry) < 2 || len(entry) > 3 {
		return nil, fmt.Errorf("entry must be [timestamp, line] or [timestamp, line, metadata]")
	}
	var nsText, line string
	if err := json.Unmarshal(entry[0], &nsText); err != nil {
		return nil, fmt.Errorf("timestamp must be a string of unix nanoseconds")
	}
	ns, err := strconv.ParseInt(nsText, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp [%s]", nsText)
	}
	if err := json.Unmarshal(entry[1], &line); err != nil {
		return nil, fmt.Errorf("line must be a string")
	}
	doc := make(map[string]interface{}, len(labels)+3)
	for k, v := range labels {
		doc[k] = v
	}
	if len(entry) == 3 {
		var metadata map[string]string
		if err := json.Unmarshal(entry[2], &metadata); err != nil {
			return nil, fmt.Errorf("structured metadata must be an object of strings")
		}
		for k, v := range metadata {
			doc[k] = v
		}
	}
	doc["message"] = line
	doc[meta.TimeFieldName] = time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
	return doc, nil
}
