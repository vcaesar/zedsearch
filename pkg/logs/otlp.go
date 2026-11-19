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
	stdjson "encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// OTLP/HTTP JSON encoding of ExportLogsServiceRequest. 64-bit integers may be
// encoded as strings or numbers, ids are hex strings.
type otlpRequest struct {
	ResourceLogs []struct {
		Resource struct {
			Attributes []otlpKeyValue `json:"attributes"`
		} `json:"resource"`
		ScopeLogs []struct {
			Scope struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"scope"`
			LogRecords []otlpLogRecord `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

type otlpLogRecord struct {
	TimeUnixNano         stdjson.Number `json:"timeUnixNano"`
	ObservedTimeUnixNano stdjson.Number `json:"observedTimeUnixNano"`
	SeverityNumber       int            `json:"severityNumber"`
	SeverityText         string         `json:"severityText"`
	Body                 *otlpAnyValue  `json:"body"`
	Attributes           []otlpKeyValue `json:"attributes"`
	TraceID              string         `json:"traceId"`
	SpanID               string         `json:"spanId"`
	EventName            string         `json:"eventName"`
}

type otlpKeyValue struct {
	Key   string        `json:"key"`
	Value *otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue *string        `json:"stringValue"`
	BoolValue   *bool          `json:"boolValue"`
	IntValue    stdjson.Number `json:"intValue"`
	DoubleValue *float64       `json:"doubleValue"`
	BytesValue  *string        `json:"bytesValue"`
	ArrayValue  *struct {
		Values []*otlpAnyValue `json:"values"`
	} `json:"arrayValue"`
	KvlistValue *struct {
		Values []otlpKeyValue `json:"values"`
	} `json:"kvlistValue"`
}

// severityNames are the OTLP SeverityNumber ranges, 1-4 TRACE ... 21-24 FATAL.
var severityNames = []string{"trace", "debug", "info", "warn", "error", "fatal"}

// SeverityText maps an OTLP SeverityNumber to a lowercase level, "" when unset.
func SeverityText(number int) string {
	if number < 1 || number > 24 {
		return ""
	}
	return severityNames[(number-1)/4]
}

// ParseOTLP decodes an OTLP/HTTP JSON logs request. Resource and record
// attributes keep their dotted keys (e.g. service.name), a string body becomes
// message and any other body is stored under body.
func ParseOTLP(body []byte) ([]map[string]interface{}, error) {
	var req otlpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid OTLP logs request: %w", err)
	}
	var docs []map[string]interface{}
	for _, rl := range req.ResourceLogs {
		resource, err := otlpAttributes(rl.Resource.Attributes)
		if err != nil {
			return nil, err
		}
		for _, sl := range rl.ScopeLogs {
			for _, rec := range sl.LogRecords {
				doc, err := otlpDocument(rec, resource)
				if err != nil {
					return nil, fmt.Errorf("log record %d: %w", len(docs), err)
				}
				if sl.Scope.Name != "" {
					doc["scope.name"] = sl.Scope.Name
				}
				if sl.Scope.Version != "" {
					doc["scope.version"] = sl.Scope.Version
				}
				docs = append(docs, doc)
			}
		}
	}
	return docs, nil
}

func otlpDocument(rec otlpLogRecord, resource map[string]interface{}) (map[string]interface{}, error) {
	doc, err := otlpAttributes(rec.Attributes)
	if err != nil {
		return nil, err
	}
	for k, v := range resource {
		if _, exists := doc[k]; !exists {
			doc[k] = v
		}
	}
	ts := rec.TimeUnixNano
	if ts == "" || ts == "0" {
		ts = rec.ObservedTimeUnixNano
	}
	if ts != "" && ts != "0" {
		ns, err := strconv.ParseInt(ts.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid timeUnixNano [%s]", ts)
		}
		doc[meta.TimeFieldName] = time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
	}
	if level := rec.SeverityText; level != "" {
		doc["level"] = level
	} else if level := SeverityText(rec.SeverityNumber); level != "" {
		doc["level"] = level
	}
	if rec.SeverityNumber != 0 {
		doc["severity_number"] = rec.SeverityNumber
	}
	if rec.Body != nil {
		body, err := rec.Body.value()
		if err != nil {
			return nil, err
		}
		if s, ok := body.(string); ok {
			doc["message"] = s
		} else if body != nil {
			doc["body"] = body
		}
	}
	for k, v := range map[string]string{"trace_id": rec.TraceID, "span_id": rec.SpanID, "event_name": rec.EventName} {
		if v != "" {
			doc[k] = v
		}
	}
	return doc, nil
}

func otlpAttributes(kvs []otlpKeyValue) (map[string]interface{}, error) {
	out := make(map[string]interface{}, len(kvs))
	for _, kv := range kvs {
		v, err := kv.Value.value()
		if err != nil {
			return nil, fmt.Errorf("attribute [%s]: %w", kv.Key, err)
		}
		if v != nil {
			out[kv.Key] = v
		}
	}
	return out, nil
}

func (a *otlpAnyValue) value() (interface{}, error) {
	switch {
	case a == nil:
		return nil, nil
	case a.StringValue != nil:
		return *a.StringValue, nil
	case a.BoolValue != nil:
		return *a.BoolValue, nil
	case a.IntValue != "":
		return strconv.ParseInt(a.IntValue.String(), 10, 64)
	case a.DoubleValue != nil:
		return *a.DoubleValue, nil
	case a.BytesValue != nil:
		return *a.BytesValue, nil // kept base64 encoded
	case a.ArrayValue != nil:
		values := make([]interface{}, 0, len(a.ArrayValue.Values))
		for _, item := range a.ArrayValue.Values {
			v, err := item.value()
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		return values, nil
	case a.KvlistValue != nil:
		return otlpAttributes(a.KvlistValue.Values)
	}
	return nil, nil
}
