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

package tokenizer

import (
	"testing"

	"github.com/go-ego/gse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot/analysis"
)

type tok struct {
	term       string
	start, end int
	incr       int
	typ        analysis.TokenType
}

func newSegmenter(t *testing.T) *gse.Segmenter {
	t.Helper()
	seg := new(gse.Segmenter)
	seg.SkipLog = true
	require.NoError(t, seg.LoadDictStr("中国 10 n\n人民 10 n\n中国人民 5 n\n"))
	return seg
}

func collect(ts analysis.TokenStream) []tok {
	out := make([]tok, 0, len(ts))
	for _, tk := range ts {
		out = append(out, tok{string(tk.Term), tk.Start, tk.End, tk.PositionIncr, tk.Type})
	}
	return out
}

func TestStandardTokenizer(t *testing.T) {
	got := collect(NewStandardTokenizer(newSegmenter(t)).Tokenize([]byte("中国人民 hi")))
	assert.Equal(t, []tok{
		{"中国人民", 0, 12, 1, analysis.Ideographic},
		{" ", 12, 13, 1, analysis.AlphaNumeric},
		{"hi", 13, 15, 1, analysis.AlphaNumeric},
	}, got)
}

func TestSearchTokenizer(t *testing.T) {
	got := collect(NewSearchTokenizer(newSegmenter(t)).Tokenize([]byte("中国人民 hi")))
	assert.Equal(t, []tok{
		{"中国", 0, 6, 0, analysis.Ideographic},
		{"人民", 6, 12, 1, analysis.Ideographic},
		{"中国人民", 0, 12, 1, analysis.Ideographic},
		{" ", 12, 13, 1, analysis.AlphaNumeric},
		{"hi", 13, 15, 1, analysis.AlphaNumeric},
	}, got)
}

func TestTokenizerEmpty(t *testing.T) {
	seg := newSegmenter(t)
	assert.Empty(t, NewStandardTokenizer(seg).Tokenize(nil))
	assert.Empty(t, NewSearchTokenizer(seg).Tokenize(nil))
}
