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

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestExcludeNestedDocuments(t *testing.T) {
	query := map[string]interface{}{"term": map[string]interface{}{"a": "b"}}
	assert.Equal(t, query, excludeNestedDocuments(query, nil))

	got := excludeNestedDocuments(nil, []string{"comments"})
	must := got.(map[string]interface{})["bool"].(map[string]interface{})["must"]
	assert.Equal(t, map[string]interface{}{"match_all": map[string]interface{}{}}, must)
}

func TestSearchHelpersInvalidInput(t *testing.T) {
	assert.Nil(t, highlightFieldAnalyzers(nil, meta.NewMappings(), nil))
	assert.Nil(t, highlightFieldAnalyzers(&meta.Highlight{}, meta.NewMappings(), nil))
	_, err := collectFieldValues(nil, map[string]interface{}{"bogus": map[string]interface{}{}}, "_id", meta.NewMappings(), nil)
	assert.Error(t, err)
	assert.True(t, isMatchIndex("abc", ""))
}

func TestUnionMappingsAnalyzers(t *testing.T) {
	indexes := make(map[string]*Index)
	for _, name := range []string{"TestUnionMappingsAnalyzers.index_1", "TestUnionMappingsAnalyzers.index_2"} {
		index, err := NewIndex(name, StorageTypeDisk, 1)
		require.NoError(t, err)
		index.ref.Mappings = meta.NewMappings()
		require.NoError(t, index.SetAnalyzers(map[string]*analysis.Analyzer{"shared": nil, name: nil}))
		indexes[name] = index
	}
	_, analyzers := unionMappings(indexes)
	assert.Len(t, analyzers, 3)
}

func TestSearch_NestedQueryErrors(t *testing.T) {
	name := "TestSearch_NestedQueryErrors.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, index.SetMappings(nestedIndexMappings(t)))
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	invalid := func() map[string]interface{} {
		return map[string]interface{}{"nested": map[string]interface{}{
			"path":  "comments",
			"query": map[string]interface{}{"bogus": map[string]interface{}{}},
		}}
	}
	_, err = index.Search(&meta.ZedQuery{Query: invalid(), Size: 10})
	assert.Error(t, err)
	_, err = MultiSearch([]string{name}, &meta.ZedQuery{Query: invalid(), Size: 10})
	assert.Error(t, err)
}

func TestMultiSearch_InvalidQuery(t *testing.T) {
	name := "TestMultiSearch_InvalidQuery.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	_, err = MultiSearch([]string{name}, &meta.ZedQuery{Query: map[string]interface{}{"bogus": map[string]interface{}{}}, Size: 10})
	assert.Error(t, err)
}

// A matched field that is stored but not asked for is not highlighted.
func TestSearch_HighlightOnlyRequestedFields(t *testing.T) {
	name := "TestSearch_HighlightOnlyRequestedFields.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	index.GetMappings().SetProperty("tags", meta.Property{Type: "text", Index: true, Highlightable: true})
	index.GetMappings().SetProperty("color", meta.Property{Type: "keyword", Index: true, Highlightable: true})

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"tags": "brown dog", "color": "brown"}, false))
	waitForDocs(t, index, 1)

	resp, err := index.Search(&meta.ZedQuery{
		Query: map[string]interface{}{"bool": map[string]interface{}{"should": []interface{}{
			map[string]interface{}{"match": map[string]interface{}{"tags": "brown"}},
			map[string]interface{}{"term": map[string]interface{}{"color": "brown"}},
		}}},
		Size: 10,
		Highlight: &meta.Highlight{
			PreTags: []string{"<b>"}, PostTags: []string{"</b>"},
			Fields: map[string]*meta.Highlight{"tags": {}},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, map[string]interface{}{"tags": []string{"<b>brown</b> dog"}}, resp.Hits.Hits[0].Highlight)
}

// A stored document whose _source and excluded vectors cannot be decoded is
// still returned, as stored.
func TestSearch_UndecodableStoredDocument(t *testing.T) {
	name := "TestSearch_UndecodableStoredDocument.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	index.GetMappings().SetProperty("title", meta.Property{Type: "text", Index: true})
	index.GetMappings().SetProperty("vec", meta.Property{Type: "vector", Index: true, Dims: 2})

	shard := anyShard(index)
	w, err := shard.GetWriter()
	require.NoError(t, err)
	doc := riot.NewDocument("raw")
	doc.AddField(riot.NewTextField("title", "brown fox").SearchTermPositions())
	doc.AddField(riot.NewStoredOnlyField("_source", []byte("not json")))
	doc.AddField(riot.NewStoredOnlyField(meta.SourceVectorsField, []byte("not json")))
	require.NoError(t, w.Insert(doc))

	hit, err := shard.FindDocumentByDocID("raw")
	require.NoError(t, err)
	assert.Equal(t, "raw", hit.ID)

	resp, err := index.Search(&meta.ZedQuery{
		Query:     &meta.Query{Match: map[string]*meta.MatchQuery{"title": {Query: "brown"}}},
		Size:      10,
		Highlight: &meta.Highlight{Fields: map[string]*meta.Highlight{"title": {}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, "raw", resp.Hits.Hits[0].ID)
	assert.Empty(t, resp.Hits.Hits[0].Highlight)
}

// A query that parses but whose searcher cannot be built fails the search.
func TestSearch_SearcherErrors(t *testing.T) {
	name := "TestSearch_SearcherErrors.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, index.SetMappings(nestedIndexMappings(t)))
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	fuzzy := func(field string) map[string]interface{} {
		return map[string]interface{}{"fuzzy": map[string]interface{}{
			field: map[string]interface{}{"value": "brown", "fuzziness": 5},
		}}
	}
	tests := []struct {
		name  string
		query map[string]interface{}
	}{
		{name: "query", query: fuzzy("title")},
		{name: "nested query", query: map[string]interface{}{"nested": map[string]interface{}{
			"path": "comments", "query": fuzzy("comments.name"),
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := index.Search(&meta.ZedQuery{Query: tt.query, Size: 10})
			assert.Error(t, err)
			_, err = MultiSearch([]string{name}, &meta.ZedQuery{Query: tt.query, Size: 10})
			assert.Error(t, err)
		})
	}
}

// A pipeline aggregation reading a missing aggregation leaves it out of the
// response, the hits are still returned.
func TestSearch_PipelineError(t *testing.T) {
	name := "TestSearch_PipelineError.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	resp, err := index.Search(&meta.ZedQuery{Query: matchAll, Size: 10, Aggregations: map[string]meta.Aggregations{
		"avg": {AvgBucket: &meta.AggregationPipeline{BucketsPath: "missing>_count"}},
	}})
	require.NoError(t, err)
	assert.NotContains(t, resp.Aggregations, "avg")
}
