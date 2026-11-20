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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/analysis/token"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/uquery/analysis/internal/testutil"
)

type synToken struct {
	term       string
	incr       int
	start, end int
}

func synTokens(ts analysis.TokenStream) []synToken {
	out := make([]synToken, 0, len(ts))
	for _, t := range ts {
		out = append(out, synToken{string(t.Term), t.PositionIncr, t.Start, t.End})
	}
	return out
}

func rules(r ...string) []interface{} {
	out := make([]interface{}, len(r))
	for i, v := range r {
		out[i] = v
	}
	return out
}

func TestSynonymTokenFilter(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]interface{}
		input   string
		want    []synToken
	}{
		{
			name:    "equivalence expand keeps original and stacks alternates",
			options: map[string]interface{}{"synonyms": rules("hello, hi, hey")},
			input:   "hello world",
			want:    []synToken{{"hello", 1, 0, 5}, {"hi", 0, 0, 5}, {"hey", 0, 0, 5}, {"world", 1, 6, 11}},
		},
		{
			name:    "equivalence expand from an alternate",
			options: map[string]interface{}{"synonyms": rules("hello, hi")},
			input:   "hi",
			want:    []synToken{{"hi", 1, 0, 2}, {"hello", 0, 0, 2}},
		},
		{
			name:    "equivalence without expand maps to the first term",
			options: map[string]interface{}{"synonyms": rules("hello, hi"), "expand": false},
			input:   "hi there hello",
			want:    []synToken{{"hello", 1, 0, 2}, {"there", 1, 3, 8}, {"hello", 1, 9, 14}},
		},
		{
			name:    "explicit mapping drops the original",
			options: map[string]interface{}{"synonyms": rules("bye, cya => goodbye, farewell")},
			input:   "cya goodbye",
			want:    []synToken{{"goodbye", 1, 0, 3}, {"farewell", 0, 0, 3}, {"goodbye", 1, 4, 11}},
		},
		{
			name:    "explicit mapping keeps the original on the right side",
			options: map[string]interface{}{"synonyms": rules("a => a, b")},
			input:   "a",
			want:    []synToken{{"a", 1, 0, 1}, {"b", 0, 0, 1}},
		},
		{
			name:    "multi-word left side matches consecutive tokens",
			options: map[string]interface{}{"synonyms": rules("ny, new york")},
			input:   "i love new york city",
			want: []synToken{
				{"i", 1, 0, 1}, {"love", 1, 2, 6},
				{"new", 1, 7, 10}, {"ny", 0, 7, 15}, {"york", 1, 11, 15},
				{"city", 1, 16, 20},
			},
		},
		{
			name:    "multi-word replacement follows positions",
			options: map[string]interface{}{"synonyms": rules("ny, new york")},
			input:   "ny city",
			want:    []synToken{{"ny", 1, 0, 2}, {"new", 0, 0, 2}, {"york", 1, 0, 2}, {"city", 1, 3, 7}},
		},
		{
			name:    "longer explicit replacement advances the following tokens",
			options: map[string]interface{}{"synonyms": rules("ny => new york")},
			input:   "ny city",
			want:    []synToken{{"new", 1, 0, 2}, {"york", 1, 0, 2}, {"city", 1, 3, 7}},
		},
		{
			name:    "every longer replacement advances the following tokens",
			options: map[string]interface{}{"synonyms": rules("a => x y z")},
			input:   "a b a c",
			want: []synToken{
				{"x", 1, 0, 1}, {"y", 1, 0, 1}, {"z", 1, 0, 1}, {"b", 1, 2, 3},
				{"x", 1, 4, 5}, {"y", 1, 4, 5}, {"z", 1, 4, 5}, {"c", 1, 6, 7},
			},
		},
		{
			name:    "same length replacement keeps the following positions",
			options: map[string]interface{}{"synonyms": rules("a b => c d")},
			input:   "a b e",
			want:    []synToken{{"c", 1, 0, 3}, {"d", 1, 0, 3}, {"e", 1, 4, 5}},
		},
		{
			name:    "escaped comma is part of the term",
			options: map[string]interface{}{"synonyms": rules(`foo\,bar => baz`)},
			input:   "foo,bar foo",
			want:    []synToken{{"baz", 1, 0, 7}, {"foo", 1, 8, 11}},
		},
		{
			name:    "escaped arrow is part of the term",
			options: map[string]interface{}{"synonyms": rules(`a\=>b, c`)},
			input:   "a=>b",
			want:    []synToken{{"a=>b", 1, 0, 4}, {"c", 0, 0, 4}},
		},
		{
			name:    "escaped backslash is unescaped",
			options: map[string]interface{}{"synonyms": rules(`x => a\\b`)},
			input:   "x",
			want:    []synToken{{`a\b`, 1, 0, 1}},
		},
		{
			// following tokens keep their positions, as Lucene's synonym_graph
			name:    "longest match first",
			options: map[string]interface{}{"synonyms": rules("new york => nyc", "new => fresh")},
			input:   "new york new",
			want:    []synToken{{"nyc", 1, 0, 8}, {"fresh", 2, 9, 12}},
		},
		{
			name:    "duplicate rules are merged",
			options: map[string]interface{}{"synonyms": rules("a, b", "b, a", "a => c")},
			input:   "a",
			want:    []synToken{{"a", 1, 0, 1}, {"b", 0, 0, 1}, {"c", 0, 0, 1}},
		},
		{
			name:    "rules are case sensitive by default",
			options: map[string]interface{}{"synonyms": rules("Hello, Hi")},
			input:   "hello",
			want:    []synToken{{"hello", 1, 0, 5}},
		},
		{
			name:    "ignore_case lowercases rules and matching",
			options: map[string]interface{}{"synonyms": rules("Hello, Hi"), "ignore_case": true},
			input:   "HELLO",
			want:    []synToken{{"HELLO", 1, 0, 5}, {"hi", 0, 0, 5}},
		},
		{
			name:    "blank and comment rules are skipped",
			options: map[string]interface{}{"synonyms": rules("", "# hello, hi", "  a, b  ")},
			input:   "hello a",
			want:    []synToken{{"hello", 1, 0, 5}, {"a", 1, 6, 7}, {"b", 0, 6, 7}},
		},
		{
			name:    "lenient skips invalid rules",
			options: map[string]interface{}{"synonyms": rules("a => b => c", "x, y"), "lenient": true},
			input:   "x a",
			want:    []synToken{{"x", 1, 0, 1}, {"y", 0, 0, 1}, {"a", 1, 2, 3}},
		},
		{
			name:    "no match",
			options: map[string]interface{}{"synonyms": rules("a, b")},
			input:   "c d",
			want:    []synToken{{"c", 1, 0, 1}, {"d", 1, 2, 3}},
		},
		{
			name:    "empty rules",
			options: map[string]interface{}{"synonyms": rules()},
			input:   "a",
			want:    []synToken{{"a", 1, 0, 1}},
		},
		{
			name:    "solr format",
			options: map[string]interface{}{"synonyms": rules("a, b"), "format": "SOLR", "updateable": true},
			input:   "a",
			want:    []synToken{{"a", 1, 0, 1}, {"b", 0, 0, 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewSynonymTokenFilter(tt.options)
			require.NoError(t, err)
			assert.Equal(t, tt.want, synTokens(f.Filter(testutil.Tokens(tt.input))))
		})
	}

	t.Run("empty input", func(t *testing.T) {
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("a, b")})
		require.NoError(t, err)
		assert.Empty(t, f.Filter(analysis.TokenStream{}))
	})

	t.Run("position gap breaks multi-word match", func(t *testing.T) {
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("new york => nyc")})
		require.NoError(t, err)
		input := analysis.TokenStream{
			{Term: []byte("new"), PositionIncr: 1, Start: 0, End: 3},
			{Term: []byte("york"), PositionIncr: 2, Start: 8, End: 12},
		}
		assert.Equal(t, []synToken{{"new", 1, 0, 3}, {"york", 2, 8, 12}}, synTokens(f.Filter(input)))
	})

	t.Run("keeps the leading position increment", func(t *testing.T) {
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("a => b")})
		require.NoError(t, err)
		input := analysis.TokenStream{{Term: []byte("a"), PositionIncr: 3, Start: 4, End: 5}}
		assert.Equal(t, []synToken{{"b", 3, 4, 5}}, synTokens(f.Filter(input)))
	})
}

func TestSynonymTokenFilterNormalized(t *testing.T) {
	stop, err := NewStopTokenFilter(map[string]interface{}{"stopwords": []interface{}{"the", "of"}})
	require.NoError(t, err)
	upper, err := NewUpperCaseTokenFilter()
	require.NoError(t, err)
	stack, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("hi, hey")})
	require.NoError(t, err)

	tests := []struct {
		name    string
		options map[string]interface{}
		pre     []analysis.TokenFilter
		input   string
		want    []string
	}{
		{
			name:    "lowercase before synonym",
			options: map[string]interface{}{"synonyms": rules("Hello, Hi")},
			pre:     []analysis.TokenFilter{token.NewLowerCaseFilter()},
			input:   "hello",
			want:    []string{"hello", "hi"},
		},
		{
			name:    "stemmer before synonym",
			options: map[string]interface{}{"synonyms": rules("running, jogging")},
			pre:     []analysis.TokenFilter{token.NewPorterStemmer()},
			input:   "jog",
			want:    []string{"jog", "run"},
		},
		{
			name:    "stop words are dropped from rule terms",
			options: map[string]interface{}{"synonyms": rules("the big apple => nyc")},
			pre:     []analysis.TokenFilter{stop},
			input:   "big apple",
			want:    []string{"nyc"},
		},
		{
			name:    "terms emptied by filters are dropped",
			options: map[string]interface{}{"synonyms": rules("the, a, b")},
			pre:     []analysis.TokenFilter{stop},
			input:   "a",
			want:    []string{"a", "b"},
		},
		{
			name:    "rules emptied by filters are skipped",
			options: map[string]interface{}{"synonyms": rules("the, of", "x => the", "the => y")},
			pre:     []analysis.TokenFilter{stop},
			input:   "the of x",
			want:    []string{"the", "of", "x"},
		},
		{
			name:    "explicit mapping normalized on both sides",
			options: map[string]interface{}{"synonyms": rules("NYC => New York")},
			pre:     []analysis.TokenFilter{token.NewLowerCaseFilter()},
			input:   "nyc",
			want:    []string{"new", "york"},
		},
		{
			name:    "ignore_case lowercases after the filters",
			options: map[string]interface{}{"synonyms": rules("hello, hi"), "ignore_case": true},
			pre:     []analysis.TokenFilter{upper},
			input:   "HELLO",
			want:    []string{"HELLO", "hi"},
		},
		{
			name:    "stacked alternates are ignored",
			options: map[string]interface{}{"synonyms": rules("hi => greeting")},
			pre:     []analysis.TokenFilter{stack},
			input:   "hi hey",
			want:    []string{"greeting", "hey"},
		},
		{
			name:    "wordnet",
			options: map[string]interface{}{"synonyms": rules("s(1,1,'Abstain',v,1,0).", "s(1,2,'Refrain',v,1,0)."), "format": "wordnet"},
			pre:     []analysis.TokenFilter{token.NewLowerCaseFilter()},
			input:   "refrain",
			want:    []string{"refrain", "abstain"},
		},
		{
			name:    "no filters keeps the rules",
			options: map[string]interface{}{"synonyms": rules("Hello, Hi")},
			input:   "Hello hello",
			want:    []string{"Hello", "Hi", "hello"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewSynonymTokenFilter(tt.options)
			require.NoError(t, err)
			syn := f.(*SynonymTokenFilter)
			n := syn.Normalized(tt.pre)
			assert.NotSame(t, syn, n)
			assert.Equal(t, tt.want, testutil.Terms(n.Filter(testutil.Tokens(tt.input))))
		})
	}

	t.Run("original filter is unchanged", func(t *testing.T) {
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("Hello, Hi")})
		require.NoError(t, err)
		f.(*SynonymTokenFilter).Normalized([]analysis.TokenFilter{token.NewLowerCaseFilter()})
		assert.Equal(t, []string{"hello"}, testutil.Terms(f.Filter(testutil.Tokens("hello"))))
		assert.Equal(t, []string{"Hello", "Hi"}, testutil.Terms(f.Filter(testutil.Tokens("Hello"))))
	})
}

func TestSynonymTokenFilterErrors(t *testing.T) {
	tests := []struct {
		name    string
		options interface{}
		want    string
	}{
		{name: "no source", options: map[string]interface{}{}, want: "requires one of"},
		{name: "nil options", options: nil, want: "requires one of"},
		{name: "synonyms not strings", options: map[string]interface{}{"synonyms": "a, b"}, want: "[synonyms] should be an array of string"},
		{name: "synonyms_set not string", options: map[string]interface{}{"synonyms_set": 1.0}, want: "[synonyms_set] should be a string"},
		{name: "synonyms_path not string", options: map[string]interface{}{"synonyms_path": 1.0}, want: "[synonyms_path] should be a string"},
		{name: "missing file", options: map[string]interface{}{"synonyms_path": "nonexistent/synonyms.txt"}, want: "failed to read synonyms_path"},
		{name: "unknown format", options: map[string]interface{}{"synonyms": rules("a, b"), "format": "xml"}, want: "unsupported format [xml]"},
		{name: "two mappings", options: map[string]interface{}{"synonyms": rules("a => b => c")}, want: "more than one explicit mapping"},
		{name: "empty term", options: map[string]interface{}{"synonyms": rules("a, , b")}, want: "term is empty"},
		{name: "empty left side", options: map[string]interface{}{"synonyms": rules("=> b")}, want: "term is empty"},
		{name: "empty right side", options: map[string]interface{}{"synonyms": rules("a =>")}, want: "term is empty"},
		{name: "bad wordnet", options: map[string]interface{}{"synonyms": rules("a, b"), "format": "wordnet"}, want: "invalid wordnet line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewSynonymTokenFilter(tt.options)
			assert.Nil(t, f)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestValidateSynonymRule(t *testing.T) {
	tests := []struct {
		rule    string
		wantErr bool
	}{
		{rule: "a, b"},
		{rule: "a"},
		{rule: "new york, ny"},
		{rule: "a, b => c"},
		{rule: `foo\,bar => baz`},
		{rule: `a\=>b => c`},
		{rule: `a\=>b => c => d`, wantErr: true},
		{rule: "a => b => c", wantErr: true},
		{rule: "", wantErr: true},
		{rule: "a,", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			err := ValidateSynonymRule(tt.rule)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSynonymTokenFilterWordnet(t *testing.T) {
	lines := rules(
		"s(100000001,1,'abstain',v,1,0).",
		"s(100000001,2,'refrain',v,1,0).",
		"s(100000002,1,'o''clock',n,1,0).",
		"s(100000002,2,'hour',n,1,0).",
		"s(100000003,1,'new york',n,1,0).",
		"s(100000003,2,'ny',n,1,0).",
	)
	tests := []struct {
		name    string
		options map[string]interface{}
		input   string
		want    []string
	}{
		{name: "expand", options: map[string]interface{}{"synonyms": lines, "format": "wordnet"}, input: "refrain", want: []string{"refrain", "abstain"}},
		{name: "quoted apostrophe", options: map[string]interface{}{"synonyms": lines, "format": "wordnet"}, input: "hour", want: []string{"hour", "o'clock"}},
		{name: "multi-word", options: map[string]interface{}{"synonyms": lines, "format": "wordnet"}, input: "ny", want: []string{"ny", "new", "york"}},
		{name: "no expand", options: map[string]interface{}{"synonyms": lines, "format": "wordnet", "expand": false}, input: "refrain", want: []string{"abstain"}},
		{
			name: "lenient skips invalid lines",
			options: map[string]interface{}{
				"synonyms": rules("bad", "s(1,1,'',v,1,0).", "s(,1,'x',v,1,0).", "s(1,1,x,v,1,0).", "s(1,1,'x,v,1,0).", "s(2,1,'a',v,1,0).", "s(2,2,'b',v,1,0)."),
				"format":   "wordnet",
				"lenient":  true,
			},
			input: "b",
			want:  []string{"b", "a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewSynonymTokenFilter(tt.options)
			require.NoError(t, err)
			assert.Equal(t, tt.want, testutil.Terms(f.Filter(testutil.Tokens(tt.input))))
		})
	}

	t.Run("empty word strict", func(t *testing.T) {
		_, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("s(1,1,'',v,1,0)."), "format": "wordnet"})
		assert.Error(t, err)
	})
	t.Run("blank word", func(t *testing.T) {
		_, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("s(1,1,' ',v,1,0)."), "format": "wordnet"})
		assert.Error(t, err)
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms": rules("s(1,1,' ',v,1,0)."), "format": "wordnet", "lenient": true})
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, testutil.Terms(f.Filter(testutil.Tokens("a"))))
	})
}

func TestSynonymTokenFilterPath(t *testing.T) {
	dir := t.TempDir()
	content := "# comment\n\nhello, hi\r\nbye => goodbye\n"
	require.NoError(t, os.Mkdir(filepath.Join(dir, SynonymsDir), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, SynonymsDir, "synonyms.txt"), []byte(content), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outside.txt"), []byte("a, b\n"), 0o600))

	oldPath := config.Global.DataPath
	config.Global.DataPath = dir
	t.Cleanup(func() { config.Global.DataPath = oldPath })

	t.Run("relative to data path", func(t *testing.T) {
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms_path": "synonyms.txt"})
		require.NoError(t, err)
		assert.Equal(t, []string{"hi", "hello", "goodbye"}, testutil.Terms(f.Filter(testutil.Tokens("hi bye"))))
	})

	t.Run("combined with inline synonyms", func(t *testing.T) {
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms_path": "synonyms.txt", "synonyms": rules("x, y")})
		require.NoError(t, err)
		assert.Equal(t, []string{"x", "y", "hello", "hi"}, testutil.Terms(f.Filter(testutil.Tokens("x hello"))))
	})

	t.Run("data path files outside the synonyms directory", func(t *testing.T) {
		_, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms_path": "outside.txt"})
		assert.ErrorContains(t, err, "failed to read synonyms_path [outside.txt]")
	})
}

func TestSynonymTokenFilterPathConfined(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "data")
	dir := filepath.Join(data, SynonymsDir)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	outside := filepath.Join(parent, "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret, value\n"), 0o600))
	files := map[string]string{
		"solr.txt":    "a, b\nsecret, , value\n",
		"wordnet.txt": "s(1,1,'a',v,1,0).\ns(1,secret\n",
		"blank.txt":   "s(1,1,'secret',v,1,0).\ns(1,2,' ',v,1,0).\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	symlink := true
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Logf("symlink unsupported: %v", err)
		symlink = false
	}

	oldPath := config.Global.DataPath
	config.Global.DataPath = data
	t.Cleanup(func() { config.Global.DataPath = oldPath })

	tests := []struct {
		name    string
		options map[string]interface{}
		want    string
	}{
		{name: "absolute", options: map[string]interface{}{"synonyms_path": outside}, want: "must be a relative path inside the [synonyms] directory of the data path"},
		{name: "parent", options: map[string]interface{}{"synonyms_path": "../secret.txt"}, want: "must be a relative path inside the [synonyms] directory of the data path"},
		{name: "nested parent", options: map[string]interface{}{"synonyms_path": "x/../../secret.txt"}, want: "must be a relative path inside the [synonyms] directory of the data path"},
		{name: "empty", options: map[string]interface{}{"synonyms_path": ""}, want: "must be a relative path inside the [synonyms] directory of the data path"},
		{name: "solr line number", options: map[string]interface{}{"synonyms_path": "solr.txt"}, want: "[synonyms_path line 2]: term is empty"},
		{name: "wordnet line number", options: map[string]interface{}{"synonyms_path": "wordnet.txt", "format": "wordnet"}, want: "[synonyms_path line 2]: invalid wordnet line"},
		{name: "wordnet blank word", options: map[string]interface{}{"synonyms_path": "blank.txt", "format": "wordnet"}, want: "[synonyms_path line 2]: invalid wordnet line"},
	}
	if symlink {
		tests = append(tests, struct {
			name    string
			options map[string]interface{}
			want    string
		}{name: "symlink escape", options: map[string]interface{}{"synonyms_path": "link.txt"}, want: "failed to read synonyms_path [link.txt]"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewSynonymTokenFilter(tt.options)
			assert.Nil(t, f)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), "secret,")
			assert.NotContains(t, err.Error(), "value")
			assert.NotContains(t, err.Error(), dir, "the data path stays private")
		})
	}
}

func TestSynonymTokenFilterSet(t *testing.T) {
	old := SynonymSetLookup
	t.Cleanup(func() { SynonymSetLookup = old })

	t.Run("nil hook", func(t *testing.T) {
		SynonymSetLookup = nil
		_, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms_set": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[synonym] synonyms set [x] not found")
	})
	t.Run("unknown set", func(t *testing.T) {
		SynonymSetLookup = func(id string) ([]string, error) { return nil, fmt.Errorf("missing %s", id) }
		_, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms_set": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[synonym] synonyms set [x] not found")
	})
	t.Run("found", func(t *testing.T) {
		SynonymSetLookup = func(id string) ([]string, error) {
			assert.Equal(t, "TestSynonymTokenFilterSet.set", id)
			return []string{"hello, hi"}, nil
		}
		f, err := NewSynonymTokenFilter(map[string]interface{}{"synonyms_set": "TestSynonymTokenFilterSet.set"})
		require.NoError(t, err)
		assert.Equal(t, []string{"hi", "hello"}, testutil.Terms(f.Filter(testutil.Tokens("hi"))))
	})
}
