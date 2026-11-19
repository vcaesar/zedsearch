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

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/riot/analysis"
)

type tok struct {
	term       string
	start, end int
}

func collect(ts analysis.TokenStream) []tok {
	out := make([]tok, 0, len(ts))
	for _, tk := range ts {
		out = append(out, tok{string(tk.Term), tk.Start, tk.End})
	}
	return out
}

func terms(ts analysis.TokenStream) []string {
	out := make([]string, 0, len(ts))
	for _, tk := range ts {
		out = append(out, string(tk.Term))
	}
	return out
}

func TestCharGroupTokenizer(t *testing.T) {
	tests := []struct {
		name  string
		chars []string
		text  string
		want  []string
	}{
		{name: "whitespace and custom", chars: []string{"whitespace", "-", ""}, text: "a-b c", want: []string{"a", "b", "c"}},
		{name: "punctuation", chars: []string{"punctuation"}, text: "a,b.c", want: []string{"a", "b", "c"}},
		{name: "digit", chars: []string{"digit"}, text: "a1b", want: []string{"a", "b"}},
		{name: "letter", chars: []string{"letter"}, text: "1a2", want: []string{"1", "2"}},
		{name: "symbol", chars: []string{"symbol"}, text: "a+b", want: []string{"a", "b"}},
		{name: "control", chars: []string{"control"}, text: "a\tb", want: []string{"a", "b"}},
		{name: "mark", chars: []string{"mark"}, text: "a\u0301b", want: []string{"a", "b"}},
		{name: "graphic", chars: []string{"graphic"}, text: "ab", want: []string{}},
		{name: "print", chars: []string{"print"}, text: "ab", want: []string{}},
		{name: "no chars", chars: nil, text: "a b", want: []string{"a b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, terms(NewCharGroupTokenizer(tt.chars).Tokenize([]byte(tt.text))))
		})
	}
}

func TestEdgeNgramTokenizer(t *testing.T) {
	tests := []struct {
		name       string
		min, max   int
		tokenChars []string
		text       string
		want       []tok
	}{
		{name: "prefixes", min: 1, max: 3, text: "abcd", want: []tok{{"a", 0, 1}, {"ab", 0, 2}, {"abc", 0, 3}}},
		{name: "min length", min: 2, max: 5, text: "abc", want: []tok{{"ab", 0, 2}, {"abc", 0, 3}}},
		{name: "token chars restart the edge", min: 1, max: 2, tokenChars: []string{"letter"}, text: "1ab", want: []tok{{"a", 1, 2}, {"ab", 1, 3}}},
		{name: "multibyte", min: 1, max: 2, text: "中文字", want: []tok{{"中", 0, 3}, {"中文", 0, 6}}},
		{name: "empty", min: 1, max: 2, text: "", want: []tok{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewEdgeNgramTokenizer(tt.min, tt.max, tt.tokenChars).Tokenize([]byte(tt.text))
			assert.Equal(t, tt.want, collect(got))
		})
	}
}

func TestNgramTokenizer(t *testing.T) {
	tests := []struct {
		name       string
		min, max   int
		tokenChars []string
		text       string
		want       []tok
	}{
		{name: "bigrams", min: 2, max: 2, text: "abc", want: []tok{{"ab", 0, 2}, {"bc", 1, 3}}},
		{name: "min and max", min: 1, max: 2, text: "ab", want: []tok{{"a", 0, 1}, {"ab", 0, 2}, {"b", 1, 2}}},
		{name: "token chars skip invalid grams", min: 2, max: 2, tokenChars: []string{"letter", "digit"}, text: "a b1", want: []tok{{"b1", 2, 4}}},
		{name: "multibyte", min: 2, max: 2, text: "中文字", want: []tok{{"中文", 0, 6}, {"文字", 3, 9}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewNgramTokenizer(tt.min, tt.max, tt.tokenChars).Tokenize([]byte(tt.text))
			assert.Equal(t, tt.want, collect(got))
		})
	}
}

func TestLowerCaseTokenizer(t *testing.T) {
	assert.Equal(t, []string{"hello", "world", "äö"}, terms(NewLowerCaseTokenizer().Tokenize([]byte("Hello, WORLD 42 ÄÖ"))))
}

func TestPathHierarchyTokenizer(t *testing.T) {
	tests := []struct {
		name        string
		delimiter   byte
		replacement byte
		skip        int
		text        string
		want        []string
	}{
		{name: "defaults", text: "/a/b/c", want: []string{"/a", "/a/b", "/a/b/c"}},
		{name: "trailing delimiter", text: "/a/b/", want: []string{"/a", "/a/b"}},
		{name: "custom delimiter and replacement", delimiter: '-', replacement: '/', text: "a-b-c", want: []string{"a", "a/b", "a/b/c"}},
		{name: "skip", skip: 1, text: "/a/b/c", want: []string{"/b", "/b/c"}},
		{name: "skip beyond depth", skip: 5, text: "/a/b", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewPathHierarchyTokenizer(tt.delimiter, tt.replacement, tt.skip).Tokenize([]byte(tt.text))
			assert.Equal(t, tt.want, terms(got))
		})
	}
}
