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
 */

package char

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMappingCharFilter_Filter(t *testing.T) {
	tests := []struct {
		name     string
		mappings []string
		text     string
		want     string
	}{
		{name: "replace", mappings: []string{"a => b", ":) => _happy_"}, text: "aa :)", want: "bb _happy_"},
		{name: "invalid mapping ignored", mappings: []string{"a=>b", "x => y => z"}, text: "ax", want: "ax"},
		{name: "no mappings", mappings: nil, text: "abc", want: "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, string(NewMappingCharFilter(tt.mappings).Filter([]byte(tt.text))))
		})
	}
}
