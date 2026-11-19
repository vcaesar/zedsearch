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
	"maps"
	"strconv"
	"strings"
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

// otlpFields is the number of fields otlpDocument and ParseOTLP add besides
// attributes.
const otlpFields = 9

func otlpDocument(rec otlpLogRecord, resource map[string]interface{}) (map[string]interface{}, error) {
	doc := make(map[string]interface{}, len(resource)+len(rec.Attributes)+otlpFields)
	maps.Copy(doc, resource)
	if err := addAttributes(doc, rec.Attributes); err != nil {
		return nil, err
	}
	ns, err := otlpTime(rec.TimeUnixNano)
	if err == nil && ns == 0 {
		ns, err = otlpTime(rec.ObservedTimeUnixNano)
	}
	if err != nil {
		return nil, err
	}
	if ns != 0 {
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
	for _, f := range [...][2]string{{"trace_id", rec.TraceID}, {"span_id", rec.SpanID}, {"event_name", rec.EventName}} {
		if f[1] != "" {
			doc[f[0]] = f[1]
		}
	}
	return doc, nil
}

// otlpTime parses a fixed64 nanosecond timestamp, 0 meaning unset.
func otlpTime(n stdjson.Number) (int64, error) {
	if n == "" {
		return 0, nil
	}
	ns, err := parseProtoInt(n.String())
	if err != nil || ns < 0 {
		return 0, fmt.Errorf("invalid timestamp [%s]", n)
	}
	return ns, nil
}

func otlpAttributes(kvs []otlpKeyValue) (map[string]interface{}, error) {
	out := make(map[string]interface{}, len(kvs))
	if err := addAttributes(out, kvs); err != nil {
		return nil, err
	}
	return out, nil
}

func addAttributes(dst map[string]interface{}, kvs []otlpKeyValue) error {
	for _, kv := range kvs {
		v, err := kv.Value.value()
		if err != nil {
			return fmt.Errorf("attribute [%s]: %w", kv.Key, err)
		}
		if v != nil {
			dst[kv.Key] = v
		}
	}
	return nil
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
		return parseProtoInt(a.IntValue.String())
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

// maxProtoExp bounds exponents so digit arithmetic can't overflow.
const maxProtoExp = 1 << 20

// parseProtoInt parses a ProtoJSON int64, which may use decimal or exponent
// notation ("1e3", "1.5E2") when the value is integral. It is exact: no
// float64 rounding.
func parseProtoInt(s string) (int64, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	invalid := fmt.Errorf("invalid integer [%s]", s)
	mant, exp := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		if exp, err = strconv.Atoi(s[i+1:]); err != nil || exp > maxProtoExp || exp < -maxProtoExp {
			return 0, invalid
		}
		mant = s[:i]
	}
	sign := ""
	if mant != "" && (mant[0] == '-' || mant[0] == '+') {
		sign, mant = mant[:1], mant[1:]
	}
	intPart, frac, _ := strings.Cut(mant, ".")
	digits := intPart + frac
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, invalid
	}
	exp -= len(frac)
	digits = strings.TrimLeft(digits, "0")
	switch {
	case digits == "":
		return 0, nil
	case exp < 0:
		// the dropped digits must all be zero for the value to be integral
		if -exp > len(digits) || strings.TrimRight(digits[len(digits)+exp:], "0") != "" {
			return 0, invalid
		}
		digits = digits[:len(digits)+exp]
	case exp > 0:
		if len(digits)+exp > 19 { // beyond int64, and keeps huge exponents cheap
			return 0, invalid
		}
		digits += strings.Repeat("0", exp)
	}
	n, err := strconv.ParseInt(sign+digits, 10, 64)
	if err != nil {
		return 0, invalid
	}
	return n, nil
}
