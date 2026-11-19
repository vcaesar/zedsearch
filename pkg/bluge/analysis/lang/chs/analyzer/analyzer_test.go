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

package analyzer

import (
	"testing"

	"github.com/go-ego/gse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSegmenter(t *testing.T) *gse.Segmenter {
	t.Helper()
	seg := new(gse.Segmenter)
	seg.SkipLog = true
	require.NoError(t, seg.LoadDictStr("中国 10 n\n人民 10 n\n中国人民 5 n\n"))
	require.NoError(t, seg.LoadStopStr("人民\n"))
	return seg
}

func TestStandardAnalyzer(t *testing.T) {
	var got []string
	for _, tk := range NewStandardAnalyzer(newSegmenter(t)).Analyze([]byte("中国人民 hi")) {
		got = append(got, string(tk.Term))
	}
	assert.Equal(t, []string{"中国人民", " ", "hi"}, got)
}

func TestSearchAnalyzer(t *testing.T) {
	var got []string
	for _, tk := range NewSearchAnalyzer(newSegmenter(t)).Analyze([]byte("中国人民 hi")) {
		got = append(got, string(tk.Term))
	}
	assert.Equal(t, []string{"中国", "中国人民", " ", "hi"}, got)
}
