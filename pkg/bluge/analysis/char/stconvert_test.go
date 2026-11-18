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

package char

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSTConvertCharFilter_Filter(t *testing.T) {
	tests := []struct {
		name       string
		conversion string
		text       string
		want       string
	}{
		{name: "s2t", conversion: "s2t", text: "汉字 abc", want: "漢字 abc"},
		{name: "t2s", conversion: "t2s", text: "漢字 abc", want: "汉字 abc"},
		{name: "empty", conversion: "s2t", text: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewSTConvertCharFilter(tt.conversion)
			assert.NoError(t, err)
			input := []byte(tt.text)
			assert.Equal(t, tt.want, string(f.Filter(input)))
			assert.Equal(t, tt.text, string(input))
		})
	}
}

func TestNewSTConvertCharFilter_Invalid(t *testing.T) {
	f, err := NewSTConvertCharFilter("xx")
	assert.Error(t, err)
	assert.Nil(t, f)
}
