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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestBoostingQuery_NegativeBoostRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"explicit zero", `{"negative_boost":0}`, `{"negative_boost":0}`},
		{"fraction", `{"negative_boost":0.5}`, `{"negative_boost":0.5}`},
		{"absent", `{}`, `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q BoostingQuery
			require.NoError(t, json.Unmarshal([]byte(tt.in), &q))
			out, err := json.Marshal(q)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(out))
		})
	}
}
