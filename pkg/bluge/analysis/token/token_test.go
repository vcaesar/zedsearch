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

package token

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/riot/analysis"
)

func stream(terms ...string) analysis.TokenStream {
	ts := make(analysis.TokenStream, 0, len(terms))
	for _, term := range terms {
		ts = append(ts, &analysis.Token{Term: []byte(term), PositionIncr: 1})
	}
	return ts
}

func terms(ts analysis.TokenStream) []string {
	out := make([]string, 0, len(ts))
	for _, tk := range ts {
		out = append(out, string(tk.Term))
	}
	return out
}

func TestRegexpTokenFilter(t *testing.T) {
	f := NewRegexpTokenFilter(regexp.MustCompile(`[0-9]+`), []byte("#"))
	assert.Equal(t, []string{"a#b", "#", "c"}, terms(f.Filter(stream("a12b", "3", "c"))))
}

func TestTrimTokenFilter(t *testing.T) {
	assert.Equal(t, []string{"a", "b c", ""}, terms(NewTrimTokenFilter().Filter(stream(" a ", "\tb c\n", "  "))))
}

func TestUpperCaseTokenFilter(t *testing.T) {
	assert.Equal(t, []string{"ABC", "ÄÖ", "1"}, terms(NewUpperCaseTokenFilter().Filter(stream("abc", "äö", "1"))))
}

func TestNewStopTokenFilter(t *testing.T) {
	t.Run("defaults to english", func(t *testing.T) {
		got := NewStopTokenFilter(nil).Filter(stream("the", "zedsearch", "and"))
		assert.Equal(t, []string{"zedsearch"}, terms(got))
	})
	t.Run("custom words and languages", func(t *testing.T) {
		got := NewStopTokenFilter([]string{"zed", "_french_"}).Filter(stream("zed", "le", "search", "the"))
		assert.Equal(t, []string{"search", "the"}, terms(got))
	})
}

func TestStopWords(t *testing.T) {
	languages := []string{
		"_ar_", "_bg_", "_bn_", "_br_", "catalan_", "_ckb_", "_cs_", "_da_", "_de_", "_el_",
		"_en_", "_es_", "_et_", "_eu_", "_fa_", "_fi_", "_fr_", "_ga_", "_gl_", "_hi_",
		"_hu_", "_hy_", "_id_", "_it_", "_lv", "_nl_", "_no_", "_pt_", "_ro_", "_ru_",
		"_sv_", "_tr_", "_th_",
	}
	for _, lang := range languages {
		t.Run(lang, func(t *testing.T) {
			words := StopWords([]string{lang})
			assert.NotEmpty(t, words)
			assert.False(t, words[lang], "language names are not stop words")
		})
	}
	t.Run("_cjk_ has no stop words", func(t *testing.T) {
		assert.Empty(t, StopWords([]string{"_cjk_"}))
	})
	t.Run("plain words", func(t *testing.T) {
		words := StopWords([]string{"foo", "bar"})
		assert.Len(t, words, 2)
		assert.True(t, words["foo"])
		assert.True(t, words["bar"])
	})
}
