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

// SourceVectorsField is the stored field listing the vectors excluded from a
// document's _source: a JSON array of their key paths, ["a","b"] for a nested
// vector and ["a.b"] for a literal key. Joined by dots a path is the name of
// the stored vector field reads restore it from, so documents written with
// and without exclusion mix. Documents cannot index a field of this name.
const SourceVectorsField = "_source_vectors"

// ExcludeSourceVectors returns source without the values of fields and the
// SourceVectorsField value listing the ones removed, nil when there is none.
// Each map on the way is copied once, source is not modified.
func ExcludeSourceVectors(source map[string]interface{}, fields []string) (map[string]interface{}, []byte, error) {
	paths := make([][]string, 0, len(fields))
	for _, field := range fields {
		if path := keyPath(source, field); path != nil {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return source, nil, nil
	}
	marker, err := json.Marshal(paths)
	if err != nil {
		return nil, nil, err
	}
	return withoutPaths(source, paths), marker, nil
}

// keyPath returns the keys leading to a dotted path, matching literal "a.b"
// keys as well as nested objects, nil when m has no such value.
func keyPath(m map[string]interface{}, path string) []string {
	if _, ok := m[path]; ok {
		return []string{path}
	}
	for i := 0; i < len(path); i++ {
		if path[i] != '.' {
			continue
		}
		if sub, ok := m[path[:i]].(map[string]interface{}); ok {
			if rest := keyPath(sub, path[i+1:]); rest != nil {
				return append([]string{path[:i]}, rest...)
			}
		}
	}
	return nil
}

// withoutPaths returns a copy of m without the values at paths, which
// keyPath found in m.
func withoutPaths(m map[string]interface{}, paths [][]string) map[string]interface{} {
	c := maps.Clone(m)
	var nested map[string][][]string
	for _, path := range paths {
		if len(path) == 1 {
			delete(c, path[0])
			continue
		}
		if nested == nil {
			nested = make(map[string][][]string)
		}
		nested[path[0]] = append(nested[path[0]], path[1:])
	}
	for key, paths := range nested {
		c[key] = withoutPaths(m[key].(map[string]interface{}), paths)
	}
	return c
}

// setPath sets value at the keys of path, the objects withoutPaths keeps.
func setPath(m map[string]interface{}, path []string, value interface{}) {
	for _, key := range path[:len(path)-1] {
		sub, ok := m[key].(map[string]interface{})
		if !ok {
			sub = make(map[string]interface{})
			m[key] = sub
		}
		m = sub
	}
	m[path[len(path)-1]] = value
}

// LookupPath returns the value at a dotted path, as keyPath matches it.
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
	excluded [][]string
	values   map[string][]byte
	err      error
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
	v.excluded, v.err = v.excluded[:0], nil
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
		v.excluded, v.err = decodeSourceVectors(value)
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
	if v == nil {
		return source, nil
	}
	if v.err != nil {
		return nil, v.err
	}
	return restoreSourceVectors(source, v.excluded, v.values)
}

// RestoreSourceVectors returns source with the vectors excluded from it,
// given the stored fields of its document by name.
func RestoreSourceVectors(source []byte, stored map[string][]byte) ([]byte, error) {
	marker, ok := stored[SourceVectorsField]
	if !ok {
		return source, nil
	}
	excluded, err := decodeSourceVectors(marker)
	if err != nil {
		return nil, err
	}
	return restoreSourceVectors(source, excluded, stored)
}

func decodeSourceVectors(marker []byte) ([][]string, error) {
	var excluded [][]string
	if err := json.Unmarshal(marker, &excluded); err != nil {
		return nil, fmt.Errorf("decode %s: %w", SourceVectorsField, err)
	}
	return excluded, nil
}

// restoreSourceVectors sets the vectors at the excluded key paths of source,
// decoded from the stored values of their fields.
func restoreSourceVectors(source []byte, excluded [][]string, values map[string][]byte) ([]byte, error) {
	if len(excluded) == 0 {
		return source, nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(source, &doc); err != nil {
		return nil, fmt.Errorf("decode _source: %w", err)
	}
	for _, path := range excluded {
		field := strings.Join(path, ".")
		raw := values[field]
		if len(raw) == 0 {
			continue // the vector field is no longer mapped
		}
		vector, err := vec.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("decode vector field [%s]: %w", field, err)
		}
		setPath(doc, path, vector)
	}
	return json.Marshal(doc)
}
