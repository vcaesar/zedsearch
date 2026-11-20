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

package assoc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type entry struct {
	text           string
	offset, length int
	options        []map[string]interface{}
}

// suggestEntries runs one named suggestion and returns its entries.
func suggestEntries(t *testing.T, body map[string]interface{}) []entry {
	t.Helper()
	rv, err := Suggest(testReaders(t), body, testMappings(), nil)
	require.NoError(t, err)
	require.Contains(t, rv, "s")
	var entries []entry
	for _, e := range rv["s"].([]interface{}) {
		e := e.(map[string]interface{})
		var options []map[string]interface{}
		for _, o := range e["options"].([]interface{}) {
			options = append(options, o.(map[string]interface{}))
		}
		entries = append(entries, entry{text: e["text"].(string), offset: e["offset"].(int), length: e["length"].(int), options: options})
	}
	return entries
}

func texts(options []map[string]interface{}) []string {
	rv := make([]string, 0, len(options))
	for _, o := range options {
		rv = append(rv, o["text"].(string))
	}
	return rv
}

func term(text string, opts map[string]interface{}) map[string]interface{} {
	if _, ok := opts["field"]; !ok {
		opts["field"] = "body"
	}
	return map[string]interface{}{"s": map[string]interface{}{"text": text, "term": opts}}
}

func TestSuggest_Term(t *testing.T) {
	t.Run("missing mode corrects unknown tokens", func(t *testing.T) {
		entries := suggestEntries(t, term("Quikc brwn fox", map[string]interface{}{}))
		require.Len(t, entries, 3)
		assert.Equal(t, entry{text: "quikc", offset: 0, length: 5, options: []map[string]interface{}{
			{"text": "quick", "score": 0.8, "freq": uint64(4)},
		}}, entries[0])
		assert.Equal(t, entry{text: "brwn", offset: 6, length: 4, options: []map[string]interface{}{
			{"text": "brown", "score": 0.75, "freq": uint64(3)},
		}}, entries[1])
		// shorter than min_word_length
		assert.Equal(t, entry{text: "fox", offset: 11, length: 3}, entries[2])
	})

	tests := []struct {
		name string
		text string
		opts map[string]interface{}
		want []string
	}{
		{"missing mode skips indexed tokens", "foxes", map[string]interface{}{}, []string{}},
		{"popular mode", "foxes", map[string]interface{}{"suggest_mode": "popular", "max_term_freq": 5, "accuracy": 0.3}, []string{"fox"}},
		{"popular needs more frequent terms", "fox", map[string]interface{}{"suggest_mode": "popular", "max_term_freq": 5, "min_word_length": 1}, []string{}},
		{"always mode", "fox", map[string]interface{}{"suggest_mode": "always", "max_term_freq": 5, "min_word_length": 1, "accuracy": 0.3}, []string{"for", "foxes"}},
		{"max_term_freq skips frequent tokens", "quick", map[string]interface{}{"suggest_mode": "always"}, []string{}},
		{"sort by score", "foxs", map[string]interface{}{}, []string{"foxes", "fox"}},
		{"sort by frequency", "foxs", map[string]interface{}{"sort": "frequency"}, []string{"fox", "foxes"}},
		{"size", "foxs", map[string]interface{}{"size": 1}, []string{"foxes"}},
		{"min_doc_freq absolute", "foxs", map[string]interface{}{"min_doc_freq": 2}, []string{"fox"}},
		{"min_doc_freq fraction", "foxs", map[string]interface{}{"min_doc_freq": 0.2}, []string{"fox"}},
		{"max_edits", "foxess", map[string]interface{}{"max_edits": 1}, []string{"foxes"}},
		{"prefix_length", "doxes", map[string]interface{}{"prefix_length": 0}, []string{"foxes"}},
		{"prefix_length blocks first char edits", "doxes", map[string]interface{}{}, []string{}},
		{"keyword field raw value", "tehc", map[string]interface{}{"field": "tag"}, []string{"tech"}},
		{"accepted options", "foxs", map[string]interface{}{"string_distance": "internal", "max_inspections": 5, "lowercase_terms": true}, []string{"foxes", "fox"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := suggestEntries(t, term(tt.text, tt.opts))
			require.Len(t, entries, 1)
			assert.Equal(t, tt.want, texts(entries[0].options))
		})
	}

	t.Run("global text", func(t *testing.T) {
		entries := suggestEntries(t, map[string]interface{}{"text": "quikc", "s": map[string]interface{}{"term": map[string]interface{}{"field": "body"}}})
		require.Len(t, entries, 1)
		assert.Equal(t, []string{"quick"}, texts(entries[0].options))
	})

	t.Run("several suggestions", func(t *testing.T) {
		rv, err := Suggest(testReaders(t), map[string]interface{}{
			"text": "quikc",
			"a":    map[string]interface{}{"term": map[string]interface{}{"field": "body"}},
			"b":    map[string]interface{}{"text": "tehc", "term": map[string]interface{}{"field": "tag"}},
		}, testMappings(), nil)
		require.NoError(t, err)
		assert.Len(t, rv, 2)
	})

	t.Run("nil mappings", func(t *testing.T) {
		rv, err := Suggest(testReaders(t), term("quikc", map[string]interface{}{}), nil, nil)
		require.NoError(t, err)
		assert.Len(t, rv["s"], 1)
	})
}

func phrase(text string, opts map[string]interface{}) map[string]interface{} {
	if _, ok := opts["field"]; !ok {
		opts["field"] = "body"
	}
	return map[string]interface{}{"s": map[string]interface{}{"text": text, "phrase": opts}}
}

func TestSuggest_Phrase(t *testing.T) {
	p := func(freq float64) float64 { return (freq + 1) / 11 }

	t.Run("corrects up to max_errors tokens", func(t *testing.T) {
		entries := suggestEntries(t, phrase("quikc brwn fox", map[string]interface{}{
			"max_errors": 2,
			"highlight":  map[string]interface{}{"pre_tag": "<em>", "post_tag": "</em>"},
		}))
		require.Len(t, entries, 1)
		assert.Equal(t, "quikc brwn fox", entries[0].text)
		assert.Equal(t, 0, entries[0].offset)
		assert.Equal(t, 14, entries[0].length)
		require.Len(t, entries[0].options, 1)
		o := entries[0].options[0]
		assert.Equal(t, "quick brown fox", o["text"])
		assert.Equal(t, "<em>quick</em> <em>brown</em> fox", o["highlighted"])
		assert.InDelta(t, 0.8*p(4)*0.75*p(3)*0.95*p(3), o["score"], 1e-12)
	})

	tests := []struct {
		name string
		text string
		opts map[string]interface{}
		want []string
	}{
		{"default max_errors is one correction", "quikc brwn fox", map[string]interface{}{"size": 2}, []string{"quick brwn fox", "quikc brown fox"}},
		{"max_errors fraction", "quikc brwn fox", map[string]interface{}{"max_errors": 0.5}, []string{"quick brwn fox"}},
		{"separator", "quikc brwn", map[string]interface{}{"separator": "|", "max_errors": 2}, []string{"quick|brown"}},
		{"confidence cutoff", "quikc brwn fox", map[string]interface{}{"confidence": 100}, []string{}},
		{"zero confidence", "quikc", map[string]interface{}{"confidence": 0}, []string{"quick"}},
		{"correct input", "quick fox", map[string]interface{}{}, []string{}},
		{"empty input", "", map[string]interface{}{}, []string{}},
		{"direct generator options", "foxs", map[string]interface{}{"real_word_error_likelihood": 0.9,
			"direct_generator": []interface{}{map[string]interface{}{"field": "body", "suggest_mode": "always", "min_doc_freq": 2}}}, []string{"fox"}},
		{"accepted options", "quikc", map[string]interface{}{"gram_size": 1, "collate": map[string]interface{}{}, "smoothing": map[string]interface{}{}}, []string{"quick"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := suggestEntries(t, phrase(tt.text, tt.opts))
			require.Len(t, entries, 1)
			assert.Equal(t, tt.want, texts(entries[0].options))
			for _, o := range entries[0].options {
				assert.NotContains(t, o, "highlighted")
			}
		})
	}
}

func completion(prefix string, opts map[string]interface{}) map[string]interface{} {
	if _, ok := opts["field"]; !ok {
		opts["field"] = "tag"
	}
	return map[string]interface{}{"s": map[string]interface{}{"prefix": prefix, "completion": opts}}
}

func ids(options []map[string]interface{}) []string {
	rv := make([]string, 0, len(options))
	for _, o := range options {
		rv = append(rv, o["_id"].(string))
	}
	return rv
}

func TestSuggest_Completion(t *testing.T) {
	t.Run("one option per document", func(t *testing.T) {
		entries := suggestEntries(t, completion("te", map[string]interface{}{}))
		require.Len(t, entries, 1)
		assert.Equal(t, "te", entries[0].text)
		assert.Equal(t, 2, entries[0].length)
		require.Len(t, entries[0].options, 4)
		assert.Equal(t, []string{"tech", "tech", "tech", "tech"}, texts(entries[0].options))
		assert.ElementsMatch(t, []string{"4", "5", "6", "10"}, ids(entries[0].options))
		o := entries[0].options[0]
		assert.Equal(t, testIndex, o["_index"])
		assert.Equal(t, 1.0, o["_score"])
		assert.Equal(t, "tech", o["_source"].(map[string]interface{})["tag"])
	})

	tests := []struct {
		name  string
		input map[string]interface{}
		want  []string
	}{
		{"size", completion("te", map[string]interface{}{"size": 2}), []string{"tech", "tech"}},
		{"skip_duplicates", completion("", map[string]interface{}{"skip_duplicates": true}), []string{"animal", "food", "tech"}},
		{"keyword is case sensitive", completion("Te", map[string]interface{}{}), []string{}},
		{"text field is lowercased", completion("Fo", map[string]interface{}{"field": "body", "skip_duplicates": true}), []string{"for", "fox", "foxes"}},
		{"fuzzy", completion("tecj", map[string]interface{}{"fuzzy": map[string]interface{}{"fuzziness": 1}, "skip_duplicates": true}), []string{"tech"}},
		{"fuzzy auto with transposition", completion("fodo", map[string]interface{}{"fuzzy": true, "skip_duplicates": true}), []string{"food"}},
		{"fuzzy exact first", completion("fo", map[string]interface{}{"field": "body", "fuzzy": map[string]interface{}{"fuzziness": 1, "min_length": 1, "prefix_length": 0}, "skip_duplicates": true, "size": 10}),
			[]string{"for", "fox", "foxes", "documents", "dog", "fast", "friends", "over"}},
		{"fuzzy below min_length", completion("tc", map[string]interface{}{"fuzzy": true}), []string{}},
		{"fuzzy transposition", completion("tceh", map[string]interface{}{"fuzzy": map[string]interface{}{"fuzziness": 1}, "size": 1}), []string{"tech"}},
		{"fuzzy without transpositions", completion("tceh", map[string]interface{}{"fuzzy": map[string]interface{}{"fuzziness": 1, "transpositions": false}}), []string{}},
		{"fuzzy disabled", completion("tecj", map[string]interface{}{"fuzzy": false}), []string{}},
		{"text input", map[string]interface{}{"s": map[string]interface{}{"text": "fo", "completion": map[string]interface{}{"field": "tag"}}}, []string{"food", "food"}},
		{"global text", map[string]interface{}{"text": "an", "s": map[string]interface{}{"completion": map[string]interface{}{"field": "tag", "size": 1}}}, []string{"animal"}},
		{"regex", map[string]interface{}{"s": map[string]interface{}{"regex": "f.*|an.*", "completion": map[string]interface{}{"field": "tag", "skip_duplicates": true}}}, []string{"animal", "food"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := suggestEntries(t, tt.input)
			require.Len(t, entries, 1)
			assert.Equal(t, tt.want, texts(entries[0].options))
		})
	}

	t.Run("fuzzy scores edits lower", func(t *testing.T) {
		entries := suggestEntries(t, completion("tecj", map[string]interface{}{"fuzzy": map[string]interface{}{"fuzziness": 1}, "size": 1}))
		assert.Equal(t, 0.5, entries[0].options[0]["_score"])
	})
}

func TestSuggest_Errors(t *testing.T) {
	readers := testReaders(t)
	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{"global text not a string", map[string]interface{}{"text": 1}},
		{"suggestion not an object", map[string]interface{}{"s": "x"}},
		{"unknown suggester", map[string]interface{}{"s": map[string]interface{}{"nope": map[string]interface{}{}}}},
		{"no suggester", map[string]interface{}{"s": map[string]interface{}{"text": "x"}}},
		{"two suggesters", map[string]interface{}{"s": map[string]interface{}{"term": map[string]interface{}{"field": "body"}, "phrase": map[string]interface{}{"field": "body"}}}},
		{"suggester not an object", map[string]interface{}{"s": map[string]interface{}{"term": "body"}}},
		{"text not a string", map[string]interface{}{"s": map[string]interface{}{"text": 1, "term": map[string]interface{}{"field": "body"}}}},
		{"prefix not a string", map[string]interface{}{"s": map[string]interface{}{"prefix": 1, "completion": map[string]interface{}{"field": "tag"}}}},
		{"term without field", term("x", map[string]interface{}{"field": ""})},
		{"term bad field", term("x", map[string]interface{}{"field": 1})},
		{"term bad sort", term("x", map[string]interface{}{"sort": "nope"})},
		{"term bad mode", term("x", map[string]interface{}{"suggest_mode": "nope"})},
		{"term max_edits", term("x", map[string]interface{}{"max_edits": 3})},
		{"term bad size", term("x", map[string]interface{}{"size": "a"})},
		{"term bad accuracy", term("x", map[string]interface{}{"accuracy": "a"})},
		{"term unknown option", term("x", map[string]interface{}{"nope": 1})},
		{"term unknown analyzer", term("x", map[string]interface{}{"analyzer": "nope"})},
		{"phrase without field", phrase("x", map[string]interface{}{"field": ""})},
		{"phrase unknown option", phrase("x", map[string]interface{}{"nope": 1})},
		{"phrase bad size", phrase("x", map[string]interface{}{"size": "a"})},
		{"phrase bad highlight", phrase("x", map[string]interface{}{"highlight": "x"})},
		{"phrase bad pre_tag", phrase("x", map[string]interface{}{"highlight": map[string]interface{}{"pre_tag": 1}})},
		{"phrase bad generator", phrase("x", map[string]interface{}{"direct_generator": "x"})},
		{"phrase empty generator", phrase("x", map[string]interface{}{"direct_generator": []interface{}{}})},
		{"phrase generator not an object", phrase("x", map[string]interface{}{"direct_generator": []interface{}{"x"}})},
		{"phrase bad generator option", phrase("x", map[string]interface{}{"direct_generator": []interface{}{map[string]interface{}{"nope": 1}}})},
		{"phrase unknown analyzer", phrase("x", map[string]interface{}{"analyzer": "nope"})},
		{"completion without field", completion("x", map[string]interface{}{"field": ""})},
		{"completion unknown option", completion("x", map[string]interface{}{"nope": 1})},
		{"completion contexts", completion("x", map[string]interface{}{"contexts": map[string]interface{}{}})},
		{"completion bad skip_duplicates", completion("x", map[string]interface{}{"skip_duplicates": "maybe"})},
		{"completion bad fuzzy", completion("x", map[string]interface{}{"fuzzy": "x"})},
		{"completion fuzzy unknown option", completion("x", map[string]interface{}{"fuzzy": map[string]interface{}{"nope": 1}})},
		{"completion fuzzy bad min_length", completion("x", map[string]interface{}{"fuzzy": map[string]interface{}{"min_length": "a"}})},
		{"completion fuzziness too big", completion("xxxx", map[string]interface{}{"fuzzy": map[string]interface{}{"fuzziness": 3}})},
		{"completion bad fuzziness", completion("xxxx", map[string]interface{}{"fuzzy": map[string]interface{}{"fuzziness": "x"}})},
		{"completion bad regex", map[string]interface{}{"s": map[string]interface{}{"regex": "[", "completion": map[string]interface{}{"field": "tag"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Suggest(readers, tt.body, testMappings(), nil)
			assert.Error(t, err)
		})
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		a, b   string
		edit   int
		prefix int
	}{
		{"quikc", "quick", 1, 1},
		{"brwn", "brown", 1, 1},
		{"", "abc", 3, 0},
		{"abc", "", 3, 3},
		{"tecj", "tech", 1, 1},
		{"tec", "technology", 7, 0},
		{"kitten", "sitting", 3, 2},
	}
	for _, tt := range tests {
		t.Run(tt.a+"/"+tt.b, func(t *testing.T) {
			assert.Equal(t, tt.edit, editDistance([]rune(tt.a), []rune(tt.b), true))
			assert.Equal(t, tt.prefix, prefixDistance([]rune(tt.a), []rune(tt.b), true))
		})
	}
	assert.Equal(t, 2, editDistance([]rune("ab"), []rune("ba"), false))
	assert.Equal(t, 1, abs(-1))
}
