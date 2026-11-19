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

package meta

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/ice/vec"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestParseVector(t *testing.T) {
	tests := []struct {
		name    string
		value   interface{}
		dims    int
		want    []float32
		wantErr bool
	}{
		{name: "interface array", value: []interface{}{1.0, 2.0, 3.0}, want: []float32{1, 2, 3}},
		{name: "interface ints", value: []interface{}{1, int64(2), 3.5}, want: []float32{1, 2, 3.5}},
		{name: "float32 array", value: []float32{1, 2}, want: []float32{1, 2}},
		{name: "float64 array", value: []float64{1, 2}, want: []float32{1, 2}},
		{name: "dims match", value: []interface{}{1.0, 2.0}, dims: 2, want: []float32{1, 2}},
		{name: "dims mismatch", value: []interface{}{1.0, 2.0}, dims: 3, wantErr: true},
		{name: "empty", value: []interface{}{}, wantErr: true},
		{name: "not numeric", value: []interface{}{"a"}, wantErr: true},
		{name: "nan", value: []float64{math.NaN()}, wantErr: true},
		{name: "inf", value: []float64{math.Inf(1)}, wantErr: true},
		{name: "unsupported type", value: "1,2,3", wantErr: true},
		{name: "unsupported scalar", value: 1.0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVector(tt.value, tt.dims)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewProperty_Vector(t *testing.T) {
	p := NewProperty("vector")
	p.Dims = 3
	assert.False(t, p.Sortable)
	assert.False(t, p.Aggregatable)
	clone := p.DeepClone()
	assert.Equal(t, 3, clone.Dims)
}

func TestExcludeSourceVectors(t *testing.T) {
	source := map[string]interface{}{
		"name":   "x",
		"emb":    []interface{}{1.0, 2.0},
		"a":      map[string]interface{}{"b": []interface{}{3.0}, "c": 1.0},
		"flat.v": []interface{}{4.0},
	}
	got, excluded, err := ExcludeSourceVectors(source, []string{"emb", "a.b", "flat.v", "missing", "name.x"})
	assert.NoError(t, err)
	assert.JSONEq(t, `[["emb"],["a","b"],["flat.v"]]`, string(excluded))
	assert.Equal(t, map[string]interface{}{"name": "x", "a": map[string]interface{}{"c": 1.0}}, got)
	// source is not modified
	assert.Contains(t, source, "emb")
	assert.Contains(t, source["a"], "b")

	same, excluded, err := ExcludeSourceVectors(source, nil)
	assert.NoError(t, err)
	assert.Nil(t, excluded)
	assert.Equal(t, source, same)

	t.Run("sibling vectors share one copy of their object", func(t *testing.T) {
		source := map[string]interface{}{"a": map[string]interface{}{"x": []interface{}{1.0}, "y": []interface{}{2.0}, "z": 3.0}}
		got, excluded, err := ExcludeSourceVectors(source, []string{"a.x", "a.y"})
		assert.NoError(t, err)
		assert.JSONEq(t, `[["a","x"],["a","y"]]`, string(excluded))
		assert.Equal(t, map[string]interface{}{"a": map[string]interface{}{"z": 3.0}}, got)
		assert.Len(t, source["a"], 3)
	})
}

// The excluded vector comes back at the key it was removed from, whatever
// the field name or the rest of the document look like.
func TestSourceVectors_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		source string
	}{
		{name: "top level", field: "emb", source: `{"emb":[1,2],"name":"x"}`},
		{name: "nested", field: "a.b", source: `{"a":{"b":[1,2],"c":1}}`},
		{name: "literal key beside an object of its prefix", field: "a.b", source: `{"a.b":[1,2],"a":{"tag":"x"}}`},
		{name: "only value of its object", field: "a.b", source: `{"a":{"b":[1,2]}}`},
		{name: "newline in the name", field: "a\nb", source: `{"a\nb":[1,2]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var source map[string]interface{}
			assert.NoError(t, json.Unmarshal([]byte(tt.source), &source))
			stripped, marker, err := ExcludeSourceVectors(source, []string{tt.field})
			assert.NoError(t, err)
			assert.NotNil(t, marker)
			stored, err := json.Marshal(stripped)
			assert.NoError(t, err)
			assert.NotContains(t, string(stored), "[1,2]")
			vector, err := vec.Encode([]float32{1, 2})
			assert.NoError(t, err)

			v := NewSourceVectors([]string{tt.field})
			v.Reset()
			assert.True(t, v.Visit(tt.field, vector))
			assert.True(t, v.Visit(SourceVectorsField, marker))
			got, err := v.Restore(stored)
			assert.NoError(t, err)
			assert.JSONEq(t, tt.source, string(got))

			got, err = RestoreSourceVectors(stored, map[string][]byte{tt.field: vector, SourceVectorsField: marker})
			assert.NoError(t, err)
			assert.JSONEq(t, tt.source, string(got))
		})
	}
}

func TestRestoreSourceVectors(t *testing.T) {
	t.Run("without marker source is unchanged", func(t *testing.T) {
		got, err := RestoreSourceVectors([]byte(`{"a":1}`), map[string][]byte{"emb": []byte("bad")})
		assert.NoError(t, err)
		assert.Equal(t, `{"a":1}`, string(got))
	})
	t.Run("errors", func(t *testing.T) {
		_, err := RestoreSourceVectors([]byte(`{}`), map[string][]byte{SourceVectorsField: []byte("emb")})
		assert.Error(t, err, "marker is not a list of key paths")
		_, err = RestoreSourceVectors([]byte(`{}`), map[string][]byte{SourceVectorsField: []byte(`[["emb"]]`), "emb": []byte("bad")})
		assert.Error(t, err, "invalid vector")
	})
}

func TestSourceVectors(t *testing.T) {
	encode := func(v []float32) []byte {
		b, err := vec.Encode(v)
		assert.NoError(t, err)
		return b
	}
	assert.Nil(t, NewSourceVectors(nil))

	t.Run("nil restores nothing", func(t *testing.T) {
		var v *SourceVectors
		v.Reset()
		assert.False(t, v.Visit(SourceVectorsField, []byte("emb")))
		assert.Nil(t, v.Fields())
		got, err := v.Restore([]byte(`{"a":1}`))
		assert.NoError(t, err)
		assert.Equal(t, `{"a":1}`, string(got))
	})

	v := NewSourceVectors([]string{"emb", "a.b", "flat.v"})
	assert.ElementsMatch(t, []string{"emb", "a.b", "flat.v", SourceVectorsField}, v.Fields())

	t.Run("restore excluded vectors", func(t *testing.T) {
		v.Reset()
		buf := encode([]float32{0.1, 2})
		assert.True(t, v.Visit("emb", buf))
		buf[len(buf)-1] = 0xff // visitors reuse buffers, values are copied
		assert.True(t, v.Visit("a.b", encode([]float32{3})))
		assert.True(t, v.Visit("flat.v", encode([]float32{4})))
		assert.False(t, v.Visit("name", []byte("x")))
		assert.True(t, v.Visit(SourceVectorsField, []byte(`[["emb"],["a","b"],["flat.v"]]`)))
		got, err := v.Restore([]byte(`{"name":"x","a":{"c":1}}`))
		assert.NoError(t, err)
		var doc map[string]interface{}
		assert.NoError(t, json.Unmarshal(got, &doc))
		assert.Equal(t, map[string]interface{}{
			"name":   "x",
			"emb":    []interface{}{0.1, 2.0},
			"a":      map[string]interface{}{"b": []interface{}{3.0}, "c": 1.0},
			"flat.v": []interface{}{4.0},
		}, doc)
	})

	t.Run("without marker source is unchanged", func(t *testing.T) {
		v.Reset()
		v.Visit("emb", encode([]float32{1}))
		got, err := v.Restore([]byte(`{"emb":[1.5]}`))
		assert.NoError(t, err)
		assert.Equal(t, `{"emb":[1.5]}`, string(got))
	})

	t.Run("unmapped excluded field is skipped", func(t *testing.T) {
		v.Reset()
		v.Visit(SourceVectorsField, []byte(`[["gone"]]`))
		got, err := v.Restore([]byte(`{"name":"x"}`))
		assert.NoError(t, err)
		assert.JSONEq(t, `{"name":"x"}`, string(got))
	})

	t.Run("reset forgets the vectors and keeps their buffers", func(t *testing.T) {
		emb := encode([]float32{7, 8})
		v.Reset()
		v.Visit("emb", emb)
		v.Reset()
		v.Visit(SourceVectorsField, []byte(`[["emb"]]`))
		got, err := v.Restore([]byte(`{"name":"x"}`))
		assert.NoError(t, err)
		assert.JSONEq(t, `{"name":"x"}`, string(got))

		// AllocsPerRun counts process-wide mallocs, background goroutines make it flaky
		buf := &v.values["emb"][:1][0]
		v.Reset()
		v.Visit("emb", emb)
		assert.Same(t, buf, &v.values["emb"][:1][0])
		assert.Equal(t, emb, v.values["emb"])
	})

	t.Run("errors", func(t *testing.T) {
		v.Reset()
		v.Visit(SourceVectorsField, []byte(`[["emb"]]`))
		v.Visit("emb", []byte("bad"))
		_, err := v.Restore([]byte(`{}`))
		assert.Error(t, err)
		_, err = v.Restore([]byte(`not json`))
		assert.Error(t, err)

		v.Reset()
		v.Visit(SourceVectorsField, []byte("emb"))
		_, err = v.Restore([]byte(`{}`))
		assert.Error(t, err, "marker is not a list of key paths")
		v.Reset()
		got, err := v.Restore([]byte(`{}`))
		assert.NoError(t, err, "reset forgets the invalid marker")
		assert.Equal(t, `{}`, string(got))
	})
}

func TestIndexSettings_ExcludeSourceVectors(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name    string
		data    string
		want    *bool
		wantErr bool
	}{
		{name: "unset", data: `{"number_of_shards": 2}`},
		{name: "native", data: `{"exclude_source_vectors": false}`, want: &no},
		{name: "es dotted", data: `{"index.mapping.exclude_source_vectors": true}`, want: &yes},
		{name: "es nested", data: `{"index": {"mapping": {"exclude_source_vectors": false}}}`, want: &no},
		{name: "es without index prefix", data: `{"mapping": {"exclude_source_vectors": "true"}}`, want: &yes},
		{name: "opensearch derived source", data: `{"index": {"knn.derived_source.enabled": false}}`, want: &no},
		{name: "native wins", data: `{"exclude_source_vectors": true, "index.mapping.exclude_source_vectors": false}`, want: &yes},
		{name: "not bool", data: `{"index.mapping.exclude_source_vectors": "maybe"}`, wantErr: true},
		{name: "native not bool", data: `{"exclude_source_vectors": "x"}`, wantErr: true},
		{name: "fractional", data: `{"index.mapping.exclude_source_vectors": 0.5}`, wantErr: true},
		{name: "number", data: `{"index": {"mapping": {"exclude_source_vectors": 1}}}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := new(IndexSettings)
			err := json.Unmarshal([]byte(tt.data), s)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, s.ExcludeSourceVectors)
		})
	}
}

func TestVectorIndexOptions_Quantization(t *testing.T) {
	assert.Equal(t, "none", (&VectorIndexOptions{Type: VectorIndexHNSW}).Quantization().String())
	assert.Equal(t, "int8", (&VectorIndexOptions{Type: VectorIndexInt8HNSW}).Quantization().String())
	assert.Equal(t, "int8", (&VectorIndexOptions{}).Quantization().String())
}

func TestMappings_VectorFields(t *testing.T) {
	m := NewMappings()
	m.SetProperty("emb", NewProperty("vector"))
	off := NewProperty("vector")
	off.Index = false
	m.SetProperty("off", off)
	m.SetProperty("name", NewProperty("text"))
	assert.Equal(t, []string{"emb"}, m.VectorFields())
}
