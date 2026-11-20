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
	"context"
	"math"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery/query"
)

// mltTerms flattens the should clauses of an MLT rewrite into field:term ->
// boost (1 when unset), in order.
func mltTerms(t *testing.T, q map[string]interface{}) ([]string, []float64) {
	t.Helper()
	b, ok := q["bool"].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	var terms []string
	var boosts []float64
	for _, c := range b["should"].([]interface{}) {
		for field, v := range c.(map[string]interface{})["term"].(map[string]interface{}) {
			v := v.(map[string]interface{})
			terms = append(terms, field+":"+v["value"].(string))
			boost := 1.0
			if bv, ok := v["boost"]; ok {
				boost = bv.(float64)
			}
			boosts = append(boosts, boost)
		}
	}
	return terms, boosts
}

func idf(df float64) float64 { return 1 + math.Log(10/(df+1)) }

func TestMoreLikeThis(t *testing.T) {
	readers := testReaders(t)
	loose := func(body map[string]interface{}) map[string]interface{} {
		body["min_term_freq"] = 1
		body["min_doc_freq"] = 1
		if _, ok := body["fields"]; !ok {
			body["fields"] = []interface{}{"body"}
		}
		return body
	}

	tests := []struct {
		name      string
		body      map[string]interface{}
		wantTerms []string
		wantIDs   []string
		mustNot   []interface{}
	}{
		{
			name:      "like text ranked by tf idf",
			body:      loose(map[string]interface{}{"like": "quick quick fox fox lazy dog search"}),
			wantTerms: []string{"body:fox", "body:quick", "body:lazy", "body:dog", "body:search"},
		},
		{
			name:      "max_query_terms",
			body:      loose(map[string]interface{}{"like": "quick quick fox fox lazy dog search", "max_query_terms": 3}),
			wantTerms: []string{"body:fox", "body:quick", "body:lazy"},
		},
		{
			name:      "default min_doc_freq filters every term",
			body:      map[string]interface{}{"fields": []interface{}{"body"}, "like": "quick quick fox fox"},
			wantTerms: nil,
		},
		{
			name:      "min_doc_freq and min_term_freq",
			body:      map[string]interface{}{"fields": []interface{}{"body"}, "like": "quick quick fox lazy lazy", "min_doc_freq": 4},
			wantTerms: []string{"body:quick"},
		},
		{
			name:      "max_doc_freq",
			body:      loose(map[string]interface{}{"like": "quick fox lazy", "max_doc_freq": 3}),
			wantTerms: []string{"body:lazy", "body:fox"},
		},
		{
			name:      "like doc by id excludes it",
			body:      loose(map[string]interface{}{"like": []interface{}{map[string]interface{}{"_index": testIndex, "_id": "2"}}}),
			wantTerms: []string{"body:quick", "body:foxes", "body:are", "body:brown"},
			wantIDs:   []string{"1", "7", "8", "9", "10"},
			mustNot:   []interface{}{"2"},
		},
		{
			name:      "include keeps the liked doc",
			body:      loose(map[string]interface{}{"like": map[string]interface{}{"_id": "2"}, "include": true}),
			wantTerms: []string{"body:quick", "body:foxes", "body:are", "body:brown"},
			wantIDs:   []string{"1", "2", "7", "8", "9", "10"},
		},
		{
			name:      "unlike removes terms",
			body:      loose(map[string]interface{}{"like": "quick fox dog", "unlike": []interface{}{"fox", map[string]interface{}{"_id": "4"}}}),
			wantTerms: []string{"body:dog", "body:quick"},
		},
		{
			name:      "stop words",
			body:      loose(map[string]interface{}{"like": "quick fox dog", "stop_words": []interface{}{"fox"}}),
			wantTerms: []string{"body:dog", "body:quick"},
		},
		{
			name:      "min_word_length",
			body:      loose(map[string]interface{}{"like": "quick fox dog lazy", "min_word_length": 4}),
			wantTerms: []string{"body:lazy", "body:quick"},
		},
		{
			name:      "max_word_length",
			body:      loose(map[string]interface{}{"like": "quick fox dog lazy", "max_word_length": 4}),
			wantTerms: []string{"body:lazy", "body:dog", "body:fox"},
		},
		{
			name:      "keyword field uses the raw value",
			body:      loose(map[string]interface{}{"fields": []interface{}{"tag"}, "like": map[string]interface{}{"doc": map[string]interface{}{"tag": "animal"}}}),
			wantTerms: []string{"tag:animal"},
			wantIDs:   []string{"1", "2", "3", "9"},
		},
		{
			name: "default fields are text and keyword",
			body: map[string]interface{}{"min_term_freq": 1, "min_doc_freq": 1,
				"like": map[string]interface{}{"doc": map[string]interface{}{"body": "lazy lazy", "tag": "food", "n": 7}}},
			wantTerms: []string{"body:lazy", "tag:food"},
		},
		{
			name:      "mixed like items",
			body:      loose(map[string]interface{}{"like": []interface{}{"lunch", map[string]interface{}{"_id": "8"}}, "max_query_terms": 1}),
			wantTerms: []string{"body:lunch"},
			wantIDs:   []string{},
			mustNot:   []interface{}{"8"},
		},
		{
			name:      "analyzer override",
			body:      loose(map[string]interface{}{"like": "Quick quick", "analyzer": "whitespace"}),
			wantTerms: []string{"body:quick"},
		},
		{
			name:      "missing liked doc",
			body:      loose(map[string]interface{}{"like": map[string]interface{}{"_id": "404"}}),
			wantTerms: nil,
		},
		{
			name:      "liked doc of another index",
			body:      loose(map[string]interface{}{"like": map[string]interface{}{"_index": "other", "_id": "2"}}),
			wantTerms: nil,
		},
		{
			name:      "unsupported field skipped",
			body:      loose(map[string]interface{}{"fields": []interface{}{"n", "body"}, "like": "lazy", "fail_on_unsupported_field": false}),
			wantTerms: []string{"body:lazy"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := MoreLikeThis(context.Background(), readers, tt.body, testMappings(), nil)
			require.NoError(t, err)
			terms, _ := mltTerms(t, q)
			assert.Equal(t, tt.wantTerms, terms)
			if tt.wantTerms == nil {
				assert.Equal(t, map[string]interface{}{"match_none": map[string]interface{}{}}, q)
				assert.Empty(t, searchIDs(t, readers, q))
				return
			}
			b := q["bool"].(map[string]interface{})
			assert.Equal(t, "30%", b["minimum_should_match"])
			if tt.mustNot != nil {
				assert.Equal(t, []interface{}{map[string]interface{}{"ids": map[string]interface{}{"values": tt.mustNot}}}, b["must_not"])
			} else {
				assert.NotContains(t, b, "must_not")
			}
			ids := searchIDs(t, readers, q)
			if tt.wantIDs != nil {
				assert.Equal(t, tt.wantIDs, ids)
			}
		})
	}

	t.Run("boost_terms normalizes to the top term", func(t *testing.T) {
		q, err := MoreLikeThis(context.Background(), readers, loose(map[string]interface{}{"like": "quick quick fox fox", "boost_terms": 2}), testMappings(), nil)
		require.NoError(t, err)
		terms, boosts := mltTerms(t, q)
		assert.Equal(t, []string{"body:fox", "body:quick"}, terms)
		assert.InDelta(t, 2, boosts[0], 1e-9)
		assert.InDelta(t, 2*idf(4)/idf(3), boosts[1], 1e-9)
	})

	t.Run("boost scales every term", func(t *testing.T) {
		q, err := MoreLikeThis(context.Background(), readers, loose(map[string]interface{}{"like": "fox lazy", "boost": 3, "minimum_should_match": 2}), testMappings(), nil)
		require.NoError(t, err)
		_, boosts := mltTerms(t, q)
		assert.Equal(t, []float64{3, 3}, boosts)
		assert.Equal(t, 2, q["bool"].(map[string]interface{})["minimum_should_match"])
		assert.Equal(t, []string{"1"}, searchIDs(t, readers, q))
	})

	t.Run("nil mappings", func(t *testing.T) {
		q, err := MoreLikeThis(context.Background(), readers, loose(map[string]interface{}{"like": "lazy"}), nil, nil)
		require.NoError(t, err)
		terms, _ := mltTerms(t, q)
		assert.Equal(t, []string{"body:lazy"}, terms)
	})
}

func TestMoreLikeThis_SameIDInTwoIndexes(t *testing.T) {
	readers := []*riot.Reader{
		newIndexReader(t, "A", []testDoc{{"7", "shared alpha alpha", "a", 1}, {"1", "shared alpha", "a", 2}}),
		newIndexReader(t, "B", []testDoc{{"7", "shared beta beta", "b", 3}, {"2", "shared beta", "b", 4}}),
	}
	like := func(items ...interface{}) map[string]interface{} {
		return map[string]interface{}{"fields": []interface{}{"body"}, "min_term_freq": 1, "min_doc_freq": 1, "like": items}
	}
	ref := func(index, id string) map[string]interface{} {
		item := map[string]interface{}{"_id": id}
		if index != "" {
			item["_index"] = index
		}
		return item
	}
	tests := []struct {
		name      string
		body      map[string]interface{}
		wantTerms []string
		mustNot   []interface{}
		wantHits  []string
	}{
		{
			name:      "explicit index finds its own seed",
			body:      like(ref("B", "7")),
			wantTerms: []string{"body:beta", "body:shared"},
			// 7 is also A/7, only an index scoped exclusion could drop B/7
			wantHits: []string{"A/1", "A/7", "B/2", "B/7"},
		},
		{
			name:      "explicit first index",
			body:      like(ref("A", "7")),
			wantTerms: []string{"body:alpha", "body:shared"},
			wantHits:  []string{"A/1", "A/7", "B/2", "B/7"},
		},
		{
			name:      "every doc of the id liked is excluded",
			body:      like(ref("A", "7"), ref("B", "7")),
			wantTerms: []string{"body:alpha", "body:beta", "body:shared"},
			mustNot:   []interface{}{"7"},
			wantHits:  []string{"A/1", "B/2"},
		},
		{
			name:      "unique id is excluded",
			body:      like(ref("B", "2")),
			wantTerms: []string{"body:beta", "body:shared"},
			mustNot:   []interface{}{"2"},
			wantHits:  []string{"A/1", "A/7", "B/7"},
		},
		{
			name:      "id only takes the first doc",
			body:      like(ref("", "7")),
			wantTerms: []string{"body:alpha", "body:shared"},
			mustNot:   []interface{}{"7"},
			wantHits:  []string{"A/1", "B/2"},
		},
		{
			name:      "skipped seeds are not excluded",
			body:      like("beta", ref("", "404"), ref("C", "2")),
			wantTerms: []string{"body:beta"},
			wantHits:  []string{"B/2", "B/7"},
		},
		{
			name:      "include has no exclusions",
			body:      func() map[string]interface{} { b := like(ref("B", "2")); b["include"] = true; return b }(),
			wantTerms: []string{"body:beta", "body:shared"},
			wantHits:  []string{"A/1", "A/7", "B/2", "B/7"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := MoreLikeThis(context.Background(), readers, tt.body, testMappings(), nil)
			require.NoError(t, err)
			terms, _ := mltTerms(t, q)
			sort.Strings(terms)
			assert.Equal(t, tt.wantTerms, terms)
			b := q["bool"].(map[string]interface{})
			if tt.mustNot != nil {
				assert.Equal(t, []interface{}{map[string]interface{}{"ids": map[string]interface{}{"values": tt.mustNot}}}, b["must_not"])
			} else {
				assert.NotContains(t, b, "must_not")
			}
			parsed, err := query.Query(q, testMappings(), nil)
			require.NoError(t, err)
			docs, err := searchDocs(context.Background(), readers, parsed, 100)
			require.NoError(t, err)
			hits := make([]string, 0, len(docs))
			for _, d := range docs {
				hits = append(hits, d.Index+"/"+d.ID)
			}
			sort.Strings(hits)
			assert.Equal(t, tt.wantHits, hits)
		})
	}
}

func TestMoreLikeThis_Errors(t *testing.T) {
	readers := testReaders(t)
	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{"missing like", map[string]interface{}{"fields": []interface{}{"body"}}},
		{"empty like", map[string]interface{}{"like": []interface{}{}}},
		{"unknown param", map[string]interface{}{"like": "x", "nope": 1}},
		{"bad like type", map[string]interface{}{"like": 1}},
		{"bad nested like", map[string]interface{}{"like": []interface{}{1}}},
		{"like item without id or doc", map[string]interface{}{"like": map[string]interface{}{"_index": "x"}}},
		{"like item unknown key", map[string]interface{}{"like": map[string]interface{}{"_id": "1", "nope": 1}}},
		{"like doc not an object", map[string]interface{}{"like": map[string]interface{}{"doc": "x"}}},
		{"like id not a string", map[string]interface{}{"like": map[string]interface{}{"_id": 1}}},
		{"bad unlike", map[string]interface{}{"like": "x", "unlike": 1}},
		{"bad fields", map[string]interface{}{"like": "x", "fields": 1}},
		{"bad min_term_freq", map[string]interface{}{"like": "x", "min_term_freq": "a"}},
		{"bad max_query_terms", map[string]interface{}{"like": "x", "max_query_terms": 0}},
		{"bad min_doc_freq", map[string]interface{}{"like": "x", "min_doc_freq": true}},
		{"bad max_doc_freq", map[string]interface{}{"like": "x", "max_doc_freq": "a"}},
		{"bad min_word_length", map[string]interface{}{"like": "x", "min_word_length": "a"}},
		{"bad max_word_length", map[string]interface{}{"like": "x", "max_word_length": "a"}},
		{"bad stop_words", map[string]interface{}{"like": "x", "stop_words": 1}},
		{"bad boost_terms", map[string]interface{}{"like": "x", "boost_terms": "a"}},
		{"bad include", map[string]interface{}{"like": "x", "include": "maybe"}},
		{"bad boost", map[string]interface{}{"like": "x", "boost": "a"}},
		{"bad analyzer type", map[string]interface{}{"like": "x", "analyzer": 1}},
		{"unknown analyzer", map[string]interface{}{"like": "x", "analyzer": "nope", "fields": []interface{}{"body"}}},
		{"bad fail_on_unsupported_field", map[string]interface{}{"like": "x", "fail_on_unsupported_field": "maybe"}},
		{"unsupported field", map[string]interface{}{"like": "x", "fields": []interface{}{"n"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MoreLikeThis(context.Background(), readers, tt.body, testMappings(), nil)
			assert.Error(t, err)
		})
	}

	t.Run("accepted item keys", func(t *testing.T) {
		_, err := MoreLikeThis(context.Background(), readers, map[string]interface{}{"_name": "n", "like": map[string]interface{}{"_id": "1", "routing": "r"}}, meta.NewMappings(), nil)
		assert.NoError(t, err)
	})
}
