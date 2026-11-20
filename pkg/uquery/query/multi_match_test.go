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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitFieldBoost_NonFinite(t *testing.T) {
	tests := []struct {
		in    string
		field string
		boost float64
	}{
		{"name^NaN", "name^NaN", 1},
		{"name^nan", "name^nan", 1},
		{"name^Inf", "name^Inf", 1},
		{"name^+Inf", "name^+Inf", 1},
		{"name^-Inf", "name^-Inf", 1},
		{"name^0", "name", 0},
		{"name^1e3", "name", 1000},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			field, boost := splitFieldBoost(tt.in)
			assert.Equal(t, tt.field, field)
			assert.Equal(t, tt.boost, boost)
		})
	}
}
