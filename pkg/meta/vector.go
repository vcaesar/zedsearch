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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
or production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package meta

import (
	"fmt"
	"maps"
	"math"
	"strings"

	"github.com/vcaesar/ice/vec"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/zutils"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// vectorMetrics maps Elasticsearch/OpenSearch similarity names to riot metrics.
var vectorMetrics = map[string]riot.Metric{
	"cosine": riot.Cosine, "cosinesimil": riot.Cosine,
	"dot_product": riot.DotProduct, "dot": riot.DotProduct, "innerproduct": riot.DotProduct, "max_inner_product": riot.DotProduct,
	"l2_norm": riot.L2, "l2": riot.L2, "euclidean": riot.L2,
}

// ParseVectorMetric resolves an Elasticsearch similarity or OpenSearch
// space_type name to a riot metric.
func ParseVectorMetric(name string) (riot.Metric, error) {
	m, ok := vectorMetrics[strings.ToLower(name)]
	if !ok {
		return "", fmt.Errorf("unsupported similarity %q, expected cosine, dot_product or l2_norm", name)
	}
	return m, nil
}

// ParseVector accepts a vector field value as a numeric array and checks it
// against dims when dims > 0. All values must be finite.
func ParseVector(v interface{}, dims int) ([]float32, error) {
	var vec []float32
	switch v := v.(type) {
	case []float32:
		vec = v
	case []float64:
		vec = make([]float32, len(v))
		for i, f := range v {
			vec[i] = float32(f)
		}
	case []interface{}:
		vec = make([]float32, len(v))
		for i, e := range v {
			f, err := zutils.ToFloat64(e)
			if err != nil {
				return nil, fmt.Errorf("invalid vector element [%v] at %d, expected number", e, i)
			}
			vec[i] = float32(f)
		}
	default:
		return nil, fmt.Errorf("unsupported vector value type %T, expected numeric array", v)
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("vector must not be empty")
	}
	for i, f := range vec {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nil, fmt.Errorf("vector element at %d is not finite", i)
		}
	}
	if dims > 0 && len(vec) != dims {
		return nil, fmt.Errorf("vector has %d dimensions, mapping expects %d", len(vec), dims)
	}
	return vec, nil
}

// SourceVectorsField is the stored field listing, one per line, the vector
// fields excluded from a document's _source. Reads restore them from the
// stored vector fields, so documents written with and without exclusion mix.
const SourceVectorsField = "_source_vectors"

// ExcludeSourceVectors returns source without the values at fields and the
// fields it removed. Maps on the way are copied, source is not modified.
func ExcludeSourceVectors(source map[string]interface{}, fields []string) (map[string]interface{}, []string) {
	var excluded []string
	for _, field := range fields {
		var ok bool
		if source, ok = withoutPath(source, field); ok {
			excluded = append(excluded, field)
		}
	}
	return source, excluded
}

// withoutPath removes a dotted path, matching literal "a.b" keys as well as
// nested objects.
func withoutPath(m map[string]interface{}, path string) (map[string]interface{}, bool) {
	if _, ok := m[path]; ok {
		c := maps.Clone(m)
		delete(c, path)
		return c, true
	}
	for i := 0; i < len(path); i++ {
		if path[i] != '.' {
			continue
		}
		sub, ok := m[path[:i]].(map[string]interface{})
		if !ok {
			continue
		}
		if sub, ok = withoutPath(sub, path[i+1:]); ok {
			c := maps.Clone(m)
			c[path[:i]] = sub
			return c, true
		}
	}
	return m, false
}

// setPath sets a dotted path below the deepest existing object, which
// withoutPath keeps, else as a literal key.
func setPath(m map[string]interface{}, path string, value interface{}) {
	for i := 0; i < len(path); i++ {
		if path[i] != '.' {
			continue
		}
		if sub, ok := m[path[:i]].(map[string]interface{}); ok {
			setPath(sub, path[i+1:], value)
			return
		}
	}
	m[path] = value
}

// LookupPath returns the value at a dotted path, as withoutPath matches it.
func LookupPath(m map[string]interface{}, path string) (interface{}, bool) {
	if v, ok := m[path]; ok {
		return v, true
	}
	for i := 0; i < len(path); i++ {
		if path[i] != '.' {
			continue
		}
		if sub, ok := m[path[:i]].(map[string]interface{}); ok {
			if v, ok := LookupPath(sub, path[i+1:]); ok {
				return v, true
			}
		}
	}
	return nil, false
}

// SourceVectors restores the vectors excluded from _source while the stored
// fields of documents are visited: Reset, Visit each field, then Restore.
// A nil SourceVectors, for mappings without vector fields, restores nothing.
type SourceVectors struct {
	fields   map[string]struct{}
	excluded []string
	values   map[string][]byte
}

// NewSourceVectors returns a SourceVectors for the vector fields, nil when
// there are none.
func NewSourceVectors(fields []string) *SourceVectors {
	if len(fields) == 0 {
		return nil
	}
	v := &SourceVectors{fields: make(map[string]struct{}, len(fields)), values: make(map[string][]byte, len(fields))}
	for _, f := range fields {
		v.fields[f] = struct{}{}
	}
	return v
}

// Fields returns the stored fields Visit consumes.
func (v *SourceVectors) Fields() []string {
	if v == nil {
		return nil
	}
	fields := make([]string, 0, len(v.fields)+1)
	for f := range v.fields {
		fields = append(fields, f)
	}
	return append(fields, SourceVectorsField)
}

// Reset forgets the fields of the previous document.
func (v *SourceVectors) Reset() {
	if v == nil {
		return
	}
	v.excluded = v.excluded[:0]
	for field, value := range v.values {
		v.values[field] = value[:0] // Visit copies into it again
	}
}

// Visit records a stored field and reports whether it was consumed. Values
// are copied, visitors reuse their buffers.
func (v *SourceVectors) Visit(field string, value []byte) bool {
	if v == nil {
		return false
	}
	if field == SourceVectorsField {
		v.excluded = append(v.excluded, strings.Split(string(value), "\n")...)
		return true
	}
	if _, ok := v.fields[field]; !ok {
		return false
	}
	v.values[field] = append(v.values[field][:0], value...)
	return true
}

// Restore returns source with the excluded vectors of the visited document,
// unchanged when it had none.
func (v *SourceVectors) Restore(source []byte) ([]byte, error) {
	if v == nil || len(v.excluded) == 0 {
		return source, nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(source, &doc); err != nil {
		return nil, fmt.Errorf("decode _source: %w", err)
	}
	for _, field := range v.excluded {
		raw := v.values[field]
		if len(raw) == 0 {
			continue // the vector field is no longer mapped
		}
		values, err := vec.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("decode vector field [%s]: %w", field, err)
		}
		setPath(doc, field, values)
	}
	return json.Marshal(doc)
}
