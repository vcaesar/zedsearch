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

package query

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptScoreQuery_Painless(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   map[string]float64
	}{
		{"not empty", "!doc['age'].empty ? 1 : 0", map[string]float64{"1": 1, "2": 1, "3": 1}},
		{"empty missing", "doc['missing'].empty ? 2 : 0", map[string]float64{"1": 2, "2": 2, "3": 2}},
		{"empty and", "doc['age'].empty && true ? 0 : 3", map[string]float64{"1": 3, "2": 3, "3": 3}},
		{"space before bracket", "doc ['age'].value", map[string]float64{"1": 1, "2": 10, "3": 100}},
		{"spaces everywhere", `doc [ "age" ].value + doc [ 'age' ].size()`, map[string]float64{"1": 2, "2": 11, "3": 101}},
		{"quote in block comment", "/* user's note */ doc['age'].empty ? 0 : 1", map[string]float64{"1": 1, "2": 1, "3": 1}},
		{"quote in line comment", "// user's note\ndoc['age'].empty ? 0 : 1", map[string]float64{"1": 1, "2": 1, "3": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scores, _ := rankScores(t, obj{"script_score": obj{"query": obj{"match_all": obj{}}, "script": tt.script}})
			assert.Len(t, scores, len(tt.want))
			for id, want := range tt.want {
				assert.InDelta(t, want, scores[id], 1e-9, id)
			}
		})
	}
}

func TestPainlessToExpr(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"!doc['age'].empty", "!(doc['age'].size == 0)"},
		{"doc ['a'].size() + doc['b'].size()", "doc ['a'].size + doc['b'].size"},
		{`params.label == "doc['age'].empty" ? 1 : 0`, `params.label == "doc['age'].empty" ? 1 : 0`},
		{`'x.size()' + doc['a'].empty`, `'x.size()' + (doc['a'].size == 0)`},
		{`"a \"doc['b'].empty\"" + doc['c'].size()`, `"a \"doc['b'].empty\"" + doc['c'].size`},
		{`/* user's note */ doc['age'].empty ? 0 : 1`, `/* user's note */ (doc['age'].size == 0) ? 0 : 1`},
		{"// user's note\ndoc['age'].empty ? 0 : 1", "// user's note\n(doc['age'].size == 0) ? 0 : 1"},
		{`/* doc['a'].empty */ doc['b'].size()`, `/* doc['a'].empty */ doc['b'].size`},
		{`"a /* b" + doc['c'].size() + "*/"`, `"a /* b" + doc['c'].size + "*/"`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, painlessToExpr(tt.in))
		})
	}
}

func TestScriptDocField(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"doc['a'].value", []string{"a"}},
		{`doc ["b"].value`, []string{"b"}},
		{"doc  [ 'c' ].size() + doc\t['d'].empty", []string{"c", "d"}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var got []string
			for _, m := range scriptDocField.FindAllStringSubmatch(tt.in, -1) {
				got = append(got, m[1])
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestScriptSigmoid_Stable(t *testing.T) {
	sigmoid, ok := scriptFunctions["sigmoid"].(func(v, k, a interface{}) float64)
	require.True(t, ok)
	tests := []struct {
		name           string
		v, pivot, expo float64
		want           float64
	}{
		{"huge value", 1e20, 10, 20, 1},
		{"tiny value", 1e-20, 10, 20, 0},
		{"at pivot", 10, 10, 20, 0.5},
		{"ratio overflows", 1e-200, 1e200, 0.001, 1 / (1 + math.Exp(0.001*400*math.Ln10))},
		{"ratio underflows", 1e200, 1e-200, 0.001, 1 / (1 + math.Exp(-0.001*400*math.Ln10))},
		{"regular", 100, 10, 0.5, math.Sqrt(100) / (math.Sqrt(100) + math.Sqrt(10))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want, sigmoid(tt.v, tt.pivot, tt.expo), 1e-12)
		})
	}
}
