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
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery/query"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

const testIndex = "TestAssoc.index_1"

type testDoc struct {
	id   string
	body string
	tag  string
	n    float64
}

var testDocs = []testDoc{
	{"1", "the quick brown fox jumps over the lazy dog", "animal", 1},
	{"2", "quick brown foxes are quick", "animal", 2},
	{"3", "the lazy dog sleeps", "animal", 3},
	{"4", "search engines index documents", "tech", 4},
	{"5", "elasticsearch is a search engine", "tech", 5},
	{"6", "zed search is fast search", "tech", 6},
	{"7", "brown bread and butter", "food", 7},
	{"8", "quick lunch with bread", "food", 8},
	{"9", "the fox and the dog are friends", "animal", 9},
	{"10", "search for the quick fox", "tech", 10},
}

func testMappings() *meta.Mappings {
	m := meta.NewMappings()
	m.SetProperty("body", meta.NewProperty("text"))
	m.SetProperty("tag", meta.NewProperty("keyword"))
	m.SetProperty("n", meta.NewProperty("numeric"))
	m.SetProperty("@timestamp", meta.NewProperty("date"))
	return m
}

// newTestReader indexes docs into an in-memory index and returns its reader.
func newTestReader(t *testing.T, docs []testDoc) *riot.Reader {
	t.Helper()
	writer, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, writer.Close()) })
	batch := riot.NewBatch()
	for _, d := range docs {
		source, err := json.Marshal(map[string]interface{}{"body": d.body, "tag": d.tag, "n": d.n})
		require.NoError(t, err)
		doc := riot.NewDocument(d.id).
			AddField(riot.NewTextField("body", d.body)).
			AddField(riot.NewKeywordField("tag", d.tag).Aggregatable()).
			AddField(riot.NewNumericField("n", d.n).Aggregatable()).
			AddField(riot.NewStoredOnlyField("_source", source)).
			AddField(riot.NewStoredOnlyField("_index", []byte(testIndex)))
		batch.Update(riot.Identifier(d.id), doc)
	}
	require.NoError(t, writer.Batch(batch))
	reader, err := writer.Reader()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reader.Close()) })
	return reader
}

// testReaders splits the fixture over two readers, as two shards.
func testReaders(t *testing.T) []*riot.Reader {
	t.Helper()
	return []*riot.Reader{newTestReader(t, testDocs[:5]), newTestReader(t, testDocs[5:])}
}

// searchIDs runs a query DSL map over readers and returns the matched ids.
func searchIDs(t *testing.T, readers []*riot.Reader, dsl map[string]interface{}) []string {
	t.Helper()
	q, err := query.Query(dsl, testMappings(), nil)
	require.NoError(t, err)
	docs, err := searchDocs(readers, q, 100)
	require.NoError(t, err)
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.Atoi(ids[i])
		b, _ := strconv.Atoi(ids[j])
		return a < b
	})
	return ids
}

func TestHelpers(t *testing.T) {
	readers := testReaders(t)

	t.Run("totalDocs", func(t *testing.T) {
		n, err := totalDocs(readers)
		require.NoError(t, err)
		assert.Equal(t, uint64(10), n)
	})

	t.Run("docFreq sums readers", func(t *testing.T) {
		tests := []struct {
			field, term string
			want        uint64
		}{
			{"body", "quick", 4},
			{"body", "search", 4},
			{"body", "fox", 3},
			{"body", "foxes", 1},
			{"body", "missing", 0},
			{"tag", "animal", 4},
			{"tag", "Animal", 0},
			{"nofield", "x", 0},
		}
		for _, tt := range tests {
			t.Run(tt.field+"/"+tt.term, func(t *testing.T) {
				df, err := docFreq(readers, tt.field, tt.term)
				require.NoError(t, err)
				assert.Equal(t, tt.want, df)
			})
		}
	})

	t.Run("prefixTerms", func(t *testing.T) {
		terms, err := prefixTerms(readers, "body", "fo")
		require.NoError(t, err)
		assert.Equal(t, map[string]struct{}{"for": {}, "fox": {}, "foxes": {}}, terms)
		all, err := prefixTerms(readers, "tag", "")
		require.NoError(t, err)
		assert.Equal(t, map[string]struct{}{"animal": {}, "food": {}, "tech": {}}, all)
	})

	t.Run("docFreq after a 1-hit term", func(t *testing.T) {
		// the second reader holds "animal" once, then "food" and "tech" twice:
		// a multi-term ice iteration would count them once
		for term, want := range map[string]uint64{"animal": 1, "food": 2, "tech": 2} {
			df, err := docFreq(readers[1:], "tag", term)
			require.NoError(t, err)
			assert.Equal(t, want, df, term)
		}
	})

	t.Run("prefixEnd", func(t *testing.T) {
		assert.Equal(t, []byte("ab"), prefixEnd([]byte("aa")))
		assert.Equal(t, []byte("b"), prefixEnd([]byte{'a', 0xff}))
		assert.Nil(t, prefixEnd([]byte{0xff, 0xff}))
	})

	t.Run("countMatches", func(t *testing.T) {
		n, err := countMatches(readers, riot.NewTermQuery("tech").SetField("tag"))
		require.NoError(t, err)
		assert.Equal(t, uint64(4), n)
	})

	t.Run("searchDocs", func(t *testing.T) {
		docs, err := searchDocs(readers, riot.NewTermQuery("7").SetField("_id"), 1)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "7", docs[0].ID)
		assert.Equal(t, testIndex, docs[0].Index)
		assert.Equal(t, "food", docs[0].Source["tag"])
		docs, err = searchDocs(readers, riot.NewMatchAllQuery(), 3)
		require.NoError(t, err)
		assert.Len(t, docs, 3)
	})

	t.Run("fieldValues", func(t *testing.T) {
		src := map[string]interface{}{
			"a":     "x",
			"b":     []interface{}{"y", float64(2), nil, map[string]interface{}{"c": "z"}},
			"obj":   map[string]interface{}{"inner": map[string]interface{}{"leaf": "deep"}},
			"title": "multi",
			"d.e":   "dotted",
		}
		assert.Equal(t, []string{"x"}, fieldValues(src, "a"))
		assert.Equal(t, []string{"y", "2"}, fieldValues(src, "b"))
		assert.Equal(t, []string{"deep"}, fieldValues(src, "obj.inner.leaf"))
		assert.Equal(t, []string{"multi"}, fieldValues(src, "title.keyword"))
		assert.Equal(t, []string{"dotted"}, fieldValues(src, "d.e"))
		assert.Nil(t, fieldValues(src, "obj"))
		assert.Nil(t, fieldValues(src, "missing"))
		assert.Nil(t, fieldValues(nil, "a"))
	})

	t.Run("tokenize keyword", func(t *testing.T) {
		tokens := tokenize(nil, "Raw Value")
		require.Len(t, tokens, 1)
		assert.Equal(t, "Raw Value", string(tokens[0].Term))
		assert.Nil(t, tokenize(nil, ""))
	})

	t.Run("fieldAnalyzer", func(t *testing.T) {
		m := testMappings()
		ana, err := fieldAnalyzer(nil, m, "tag", "", false)
		require.NoError(t, err)
		assert.Nil(t, ana)
		ana, err = fieldAnalyzer(nil, m, "body", "", true)
		require.NoError(t, err)
		assert.NotNil(t, ana)
		ana, err = fieldAnalyzer(nil, m, "tag", "whitespace", false)
		require.NoError(t, err)
		assert.NotNil(t, ana)
		_, err = fieldAnalyzer(nil, m, "body", "nope", false)
		assert.Error(t, err)
	})

	t.Run("runeOffset", func(t *testing.T) {
		assert.Equal(t, 2, runeOffset("héllo", 3))
		assert.Equal(t, 5, runeOffset("héllo", 100))
	})
}

func TestParams(t *testing.T) {
	_, err := paramInt("k", "v", true)
	assert.Error(t, err)
	_, err = paramInt("k", "v", "x")
	assert.Error(t, err)
	n, err := paramInt("k", "v", float64(3))
	assert.NoError(t, err)
	assert.Equal(t, 3, n)
	_, err = paramFloat("k", "v", false)
	assert.Error(t, err)
	_, err = paramFloat("k", "v", []interface{}{})
	assert.Error(t, err)
	_, err = paramBool("k", "v", "maybe")
	assert.Error(t, err)
	_, err = paramString("k", "v", 1)
	assert.Error(t, err)
	s, err := paramStrings("k", "v", []string{"a"})
	assert.NoError(t, err)
	assert.Equal(t, []string{"a"}, s)
	_, err = paramStrings("k", "v", []interface{}{1})
	assert.Error(t, err)
	_, err = paramStrings("k", "v", 1)
	assert.Error(t, err)
}
