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

func TestRankFeatureSigmoid_Stable(t *testing.T) {
	tests := []struct {
		name           string
		v, pivot, expo float64
		want           float64
	}{
		{"huge value", 1e20, 10, 20, 1},
		{"tiny value", 1e-20, 10, 20, 0},
		{"at pivot", 10, 10, 20, 0.5},
		{"at huge pivot", 1e200, 1e200, 20, 0.5},
		{"ratio overflows", 1e-200, 1e200, 0.001, 1 / (1 + math.Exp(0.001*400*math.Ln10))},
		{"ratio underflows", 1e200, 1e-200, 0.001, 1 / (1 + math.Exp(-0.001*400*math.Ln10))},
		{"regular", 100, 10, 0.5, math.Sqrt(100) / (math.Sqrt(100) + math.Sqrt(10))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, err := rankFeatureFunc("sigmoid", obj{"pivot": tt.pivot, "exponent": tt.expo})
			require.NoError(t, err)
			assert.InDelta(t, tt.want, fn(tt.v), 1e-12)
		})
	}
}
