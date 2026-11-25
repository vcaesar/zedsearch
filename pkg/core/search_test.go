/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI.
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
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis/analyzer"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestIndex_Search(t *testing.T) {
	type args struct {
		iQuery *meta.ZedQuery
	}
	tests := []struct {
		name    string
		args    args
		want    *meta.SearchResponse
		wantNum int
		wantErr bool
	}{
		{
			name: "Search Query - Match",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						Match: map[string]*meta.MatchQuery{
							"_all": {
								Query: "Prabhat",
							},
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - Term",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						Term: map[string]*meta.TermQuery{
							"_all": {
								Value: "angeles",
							},
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - MatchAll",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						MatchAll: &meta.MatchAllQuery{},
					},
					Size: 10,
				},
			},
			wantNum: 3,
		},
		{
			name: "Search Query - wildcard",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						Wildcard: map[string]*meta.WildcardQuery{
							"_all": {
								Value: "san*",
							},
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - fuzzy",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						Fuzzy: map[string]*meta.FuzzyQuery{
							"_all": {
								Value: "fransisco", // note the wrong spelling
							},
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - fuzzy fuzziness AUTO",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						Fuzzy: map[string]*meta.FuzzyQuery{
							"_all": {
								Value:     "fransisco", // note the wrong spelling,
								Fuzziness: "AUTO",
							},
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - fuzzy fuzziness AUTO",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						Fuzzy: map[string]*meta.FuzzyQuery{
							"_all": {
								Value:     "fransisco", // note the wrong spelling,
								Fuzziness: "AUTO:3,6",
							},
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - fuzzy fuzziness 2",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						Fuzzy: map[string]*meta.FuzzyQuery{
							"_all": {
								Value:     "fransisco", // note the wrong spelling,
								Fuzziness: 2,
							},
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - querystring",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						QueryString: &meta.QueryStringQuery{
							Query: "angeles",
						},
					},
					Size: 10,
				},
			},
		},
		{
			name: "Search Query - highlight",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						QueryString: &meta.QueryStringQuery{
							Query: "angeles",
						},
					},
					Timeout: 1,
					Size:    10,
					Fields:  []interface{}{"address.city"},
					Highlight: &meta.Highlight{
						Fields: map[string]*meta.Highlight{
							"address.city": {
								PreTags:  []string{"<b>"},
								PostTags: []string{"</b>"},
							},
						},
					},
				},
			},
		},
		{
			name: "Search Query - aggs",
			args: args{
				iQuery: &meta.ZedQuery{
					Query: &meta.Query{
						MatchAll: &meta.MatchAllQuery{},
					},
					Timeout: 1,
					Size:    0,
					Aggregations: map[string]meta.Aggregations{
						"hobby": {
							Terms: &meta.AggregationsTerms{
								Field: "hobby",
							},
						},
					},
				},
			},
		},
	}

	prepareData := []map[string]interface{}{
		{
			"name": "Prabhat Sharma",
			"address": map[string]interface{}{
				"city":  "San Francisco",
				"state": "California",
			},
			"hobby": "chess",
		},
		{
			"name": "Leonardo DiCaprio",
			"address": map[string]interface{}{
				"city":  "Los angeles",
				"state": "California",
			},
			"hobby": "chess",
		},
		{
			"name": "Baris DiCaprio",
			"address": map[string]interface{}{
				"city":  "Los angeles",
				"state": "California",
			},
			"hobby": "chess",
		},
	}

	var err error
	var index *Index
	indexName := "Search.v2.index_1"
	t.Run("Prepare", func(t *testing.T) {
		index, err = NewIndex(indexName, "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)
		err = StoreIndex(index)
		assert.NoError(t, err)

		// the queries search the _all field, off by default
		index.GetMappings().SetAllEnabled(true)
		index.GetMappings().SetProperty("address.city", meta.Property{
			Type:          "text",
			Index:         true,
			Store:         true,
			Highlightable: true,
		})

		for i, d := range prepareData {
			err := index.CreateDocument(strconv.Itoa(i), d, false)
			assert.NoError(t, err)
		}

		waitForDocs(t, index, len(prepareData))
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := index.Search(tt.args.iQuery)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, got.Hits.Total.Value, 1)
			if tt.wantNum > 0 {
				assert.Equal(t, got.Hits.Total.Value, tt.wantNum)
				assert.Equal(t, len(got.Hits.Hits), tt.wantNum)
			}
		})
	}

	t.Run("Cleanup", func(t *testing.T) {
		err = DeleteIndex(indexName)
		assert.NoError(t, err)
	})
}

// waitForDocs waits until all shards have consumed the WAL and n documents are searchable.
func waitForDocs(t *testing.T, index *Index, n int) {
	t.Helper()
	query := &meta.ZedQuery{Query: &meta.Query{MatchAll: &meta.MatchAllQuery{}}, Size: n + 1}
	assert.Eventually(t, func() bool {
		res, err := index.Search(query)
		return err == nil && res.Hits.Total.Value == n
	}, 30*time.Second, 50*time.Millisecond, "expected %d documents to be indexed", n)
}

func TestSearchV2_ResetsFieldsPerHit(t *testing.T) {
	w, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	defer func() { assert.NoError(t, w.Close()) }()

	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	batch := riot.NewBatch()
	first := riot.NewDocument("a").
		AddField(riot.NewNumericField("n", 1).Sortable()).
		AddField(riot.NewStoredOnlyField("_index", []byte("idx"))).
		AddField(riot.NewDateTimeField("@timestamp", ts).StoreValue()).
		AddField(riot.NewStoredOnlyField("_source", []byte(`{"x":1}`)))
	second := riot.NewDocument("b").
		AddField(riot.NewNumericField("n", 2).Sortable()).
		AddField(riot.NewStoredOnlyField("_source", []byte(`{"x":2}`)))
	batch.Update(first.ID(), first)
	batch.Update(second.ID(), second)
	require.NoError(t, w.Batch(batch))

	r, err := w.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, r.Close()) }()
	req := riot.NewTopNSearch(10, riot.NewMatchAllQuery()).SortBy([]string{"n"}).WithStandardAggregations()
	dmi, err := r.Search(context.Background(), req)
	require.NoError(t, err)

	query := &meta.ZedQuery{Source: &meta.Source{Enable: true}}
	resp, err := searchV2(1, 1, dmi, query, meta.NewMappings(), nil, nil)
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 2)

	got := resp.Hits.Hits
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, "idx", got[0].Index)
	assert.True(t, ts.Equal(got[0].Timestamp))
	assert.Equal(t, float64(1), got[0].Source.(map[string]interface{})["x"])

	assert.Equal(t, "b", got[1].ID)
	assert.Empty(t, got[1].Index, "_index must not leak from the previous hit")
	assert.True(t, got[1].Timestamp.IsZero(), "@timestamp must not leak from the previous hit")
	assert.Equal(t, float64(2), got[1].Source.(map[string]interface{})["x"])
}

func TestIndex_Search_HighlightWithoutOffsets(t *testing.T) {
	indexName := "TestIndex_Search_HighlightWithoutOffsets.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	index.GetMappings().SetProperty("body", meta.Property{Type: "text", Index: true, Highlightable: true})

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"body": "Quick fox, quick brown fox"}, false))
	waitForDocs(t, index, 1)

	tests := []struct {
		name  string
		query *meta.Query
		want  string
	}{
		{
			name:  "match",
			query: &meta.Query{Match: map[string]*meta.MatchQuery{"body": {Query: "brown"}}},
			want:  "Quick fox, quick <b>brown</b> fox",
		},
		{
			name:  "phrase marks only the phrase",
			query: &meta.Query{MatchPhrase: map[string]*meta.MatchPhraseQuery{"body": {Query: "quick brown"}}},
			want:  "Quick fox, <b>quick</b> <b>brown</b> fox",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := index.Search(&meta.ZedQuery{Query: tt.query, Size: 10, Highlight: &meta.Highlight{
				Fields: map[string]*meta.Highlight{"body": {PreTags: []string{"<b>"}, PostTags: []string{"</b>"}}},
			}})
			require.NoError(t, err)
			require.Len(t, resp.Hits.Hits, 1)
			assert.Equal(t, []string{tt.want}, resp.Hits.Hits[0].Highlight["body"])
		})
	}
}

func TestIndex_Search_HighlightStoredValues(t *testing.T) {
	indexName := "TestIndex_Search_HighlightStoredValues.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	index.GetMappings().SetProperty("tags", meta.Property{Type: "text", Index: true, Highlightable: true})
	index.GetMappings().SetProperty("color", meta.Property{Type: "keyword", Index: true, Highlightable: true})

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{
		"tags":  []interface{}{"brown dog", "red fox", "brown cat"},
		"color": []interface{}{"dark brown", "red"},
	}, false))
	waitForDocs(t, index, 1)

	tests := []struct {
		name      string
		query     *meta.Query
		field     string
		fragments int // number_of_fragments of tags, 0 takes the default
		want      []string
	}{
		{
			name:  "number_of_fragments counts over the array",
			query: &meta.Query{Match: map[string]*meta.MatchQuery{"tags": {Query: "brown"}}},
			field: "tags", fragments: 1, want: []string{"<b>brown</b> dog"},
		},
		{
			name:  "first element of an array",
			query: &meta.Query{Match: map[string]*meta.MatchQuery{"tags": {Query: "dog"}}},
			field: "tags", want: []string{"brown <b>dog</b>"},
		},
		{
			name:  "every matched element of an array",
			query: &meta.Query{Match: map[string]*meta.MatchQuery{"tags": {Query: "brown"}}},
			field: "tags", want: []string{"<b>brown</b> dog", "<b>brown</b> cat"},
		},
		{
			name:  "keyword",
			query: &meta.Query{Term: map[string]*meta.TermQuery{"color": {Value: "dark brown"}}},
			field: "color", want: []string{"<b>dark brown</b>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := index.Search(&meta.ZedQuery{Query: tt.query, Size: 10, Highlight: &meta.Highlight{
				PreTags: []string{"<b>"}, PostTags: []string{"</b>"},
				Fields: map[string]*meta.Highlight{"tags": {NumberOfFragments: tt.fragments}, "color": {}},
			}})
			require.NoError(t, err)
			require.Len(t, resp.Hits.Hits, 1)
			assert.Equal(t, map[string]interface{}{tt.field: tt.want}, resp.Hits.Hits[0].Highlight)
		})
	}
}

func TestIndex_Search_HighlightFromSource(t *testing.T) {
	indexName := "TestIndex_Search_HighlightFromSource.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	// as Elasticsearch, no field is highlightable or stored
	for _, f := range []string{"body", "tags", "meta.note", "items.0.text", "code"} {
		index.GetMappings().SetProperty(f, meta.Property{Type: "text", Index: true})
	}

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{
		"code":  123.0,
		"body":  "Quick fox, quick brown fox",
		"tags":  []interface{}{"red fox", "brown dog"},
		"meta":  map[string]interface{}{"note": "a brown note"},
		"items": []interface{}{map[string]interface{}{"text": "a brown item"}},
	}, false))
	waitForDocs(t, index, 1)

	tags := map[string]*meta.Highlight{"body": {}, "tags": {}, "meta.note": {}, "items.0.text": {}, "title": {}}
	resp, err := index.Search(&meta.ZedQuery{
		Query: &meta.Query{QueryString: &meta.QueryStringQuery{Query: "body:brown tags:brown meta.note:brown items.0.text:brown"}},
		Size:  10, Highlight: &meta.Highlight{PreTags: []string{"<b>"}, PostTags: []string{"</b>"}, Fields: tags},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	h := resp.Hits.Hits[0].Highlight
	assert.Equal(t, []string{"Quick fox, quick <b>brown</b> fox"}, h["body"])
	assert.Equal(t, []string{"<b>brown</b> dog"}, h["tags"])
	assert.Equal(t, []string{"a <b>brown</b> note"}, h["meta.note"])
	assert.Equal(t, []string{"a <b>brown</b> item"}, h["items.0.text"])
	assert.NotContains(t, h, "title")
	assert.NotNil(t, resp.Hits.Hits[0].Source)

	resp, err = index.Search(&meta.ZedQuery{
		Query: &meta.Query{Match: map[string]*meta.MatchQuery{"body": {Query: "brown"}}},
		Size:  10, Highlight: &meta.Highlight{Fields: map[string]*meta.Highlight{"tags": {}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Empty(t, resp.Hits.Hits[0].Highlight, "tags did not match")

	// a number in a text field is indexed, and highlighted, as its string
	resp, err = index.Search(&meta.ZedQuery{
		Query: &meta.Query{Match: map[string]*meta.MatchQuery{"code": {Query: "123"}}},
		Size:  10, Highlight: &meta.Highlight{PreTags: []string{"<b>"}, PostTags: []string{"</b>"}, Fields: map[string]*meta.Highlight{"code": {}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, []string{"<b>123</b>"}, resp.Hits.Hits[0].Highlight["code"])

	// number_of_fragments counts over the values of the field
	resp, err = index.Search(&meta.ZedQuery{
		Query: &meta.Query{QueryString: &meta.QueryStringQuery{Query: "tags:fox tags:dog"}},
		Size:  10, Highlight: &meta.Highlight{PreTags: []string{"<b>"}, PostTags: []string{"</b>"}, Fields: map[string]*meta.Highlight{"tags": {NumberOfFragments: 1}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, []string{"red <b>fox</b>"}, resp.Hits.Hits[0].Highlight["tags"])
}

// A stored text field that another searched index maps as a vector is still
// highlighted: collecting vectors must not take the value away.
func TestSearchV2_HighlightFieldMappedAsVectorElsewhere(t *testing.T) {
	a := &Index{ref: &meta.Index{Name: "a", Mappings: meta.NewMappings()}}
	a.ref.Mappings.SetProperty("body", meta.NewProperty("vector"))
	b := &Index{ref: &meta.Index{Name: "b", Mappings: meta.NewMappings()}}
	b.ref.Mappings.SetProperty("body", meta.Property{Type: "text", Index: true, Highlightable: true})
	b.ref.Mappings.SetSourceEnabled(false)

	w, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	defer func() { assert.NoError(t, w.Close()) }()
	batch := riot.NewBatch()
	doc := riot.NewDocument("1").
		AddField(riot.NewTextField("body", "brown fox").SearchTermPositions().StoreValue()).
		AddField(riot.NewStoredOnlyField("_index", []byte("b")))
	batch.Update(doc.ID(), doc)
	require.NoError(t, w.Batch(batch))
	r, err := w.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, r.Close()) }()
	req := riot.NewTopNSearch(10, riot.NewTermQuery("fox").SetField("body")).IncludeLocations().WithStandardAggregations()
	dmi, err := r.Search(context.Background(), req)
	require.NoError(t, err)

	query := &meta.ZedQuery{Source: &meta.Source{Enable: true}, Highlight: &meta.Highlight{
		PreTags: []string{"<b>"}, PostTags: []string{"</b>"}, Fields: map[string]*meta.Highlight{"body": {NumberOfFragments: 1}},
	}}
	resp, err := searchV2(1, 1, dmi, query, a.GetMappings(), nil, map[string]*Index{"a": a, "b": b})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, []string{"brown <b>fox</b>"}, resp.Hits.Hits[0].Highlight["body"])
}

func TestSourceStrings(t *testing.T) {
	doc := map[string]interface{}{
		"a":   "x",
		"b":   []interface{}{"y", 1.0, "z"},
		"c":   map[string]interface{}{"d": "w", "e": map[string]interface{}{"f": "v"}},
		"g.h": "dotted",
		"n":   1.0,
		"items": []interface{}{
			map[string]interface{}{"text": "brown"},
			map[string]interface{}{"text": "fox", "tags": []interface{}{"t1", "t2"}},
		},
		"m": []interface{}{[]interface{}{"p", "q"}},
	}
	tests := []struct {
		field string
		want  []string
	}{
		{"a", []string{"x"}},
		{"b", []string{"y", "z"}},
		{"c.d", []string{"w"}},
		{"c.e.f", []string{"v"}},
		{"g.h", []string{"dotted"}},
		{"n", nil},
		{"c", nil},
		{"missing.x", nil},
		{"items.0.text", []string{"brown"}},
		{"items.1.text", []string{"fox"}},
		{"items.1.tags", []string{"t1", "t2"}},
		{"items.2.text", nil},
		{"items.x.text", nil},
		{"m.0", []string{"p", "q"}},
		{"b.0", []string{"y"}},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			assert.Equal(t, tt.want, sourceStrings(doc, tt.field, false))
		})
	}

	// a text or keyword field indexes numbers and booleans as strings
	doc["flag"] = true
	terms := []struct {
		field string
		want  []string
	}{
		{"n", []string{"1"}},
		{"b", []string{"y", "1", "z"}},
		{"flag", []string{"true"}},
		{"c", nil},
	}
	for _, tt := range terms {
		t.Run("terms "+tt.field, func(t *testing.T) {
			assert.Equal(t, tt.want, sourceStrings(doc, tt.field, true))
		})
	}
}

func TestSourceField(t *testing.T) {
	mappings := meta.NewMappings()
	body := meta.NewProperty("text")
	body.AddField("raw", meta.NewProperty("keyword"))
	mappings.SetProperty("body", body)
	mappings.SetProperty("body.raw", meta.NewProperty("keyword"))
	mappings.SetProperty("meta.note", meta.NewProperty("text"))
	for field, want := range map[string]string{
		"body":      "body",
		"body.raw":  "body",
		"body.x":    "body.x",
		"meta.note": "meta.note",
		"missing.y": "missing.y",
	} {
		assert.Equal(t, want, sourceField(mappings, field), field)
	}
}

func TestIndex_Search_HighlightMultiField(t *testing.T) {
	indexName := "TestIndex_Search_HighlightMultiField.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	// keyword fields keep no term locations, a text multi-field does
	raw := meta.Property{Type: "text", Index: true, Analyzer: "keyword"}
	body := meta.Property{Type: "text", Index: true}
	body.AddField("raw", raw)
	index.GetMappings().SetProperty("body", body)
	index.GetMappings().SetProperty("body.raw", raw)

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"body": "QuickFox"}, false))
	waitForDocs(t, index, 1)

	resp, err := index.Search(&meta.ZedQuery{
		Query: &meta.Query{Term: map[string]*meta.TermQuery{"body.raw": {Value: "QuickFox"}}},
		Size:  10, Highlight: &meta.Highlight{PreTags: []string{"<b>"}, PostTags: []string{"</b>"},
			Fields: map[string]*meta.Highlight{"body.raw": {}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, []string{"<b>QuickFox</b>"}, resp.Hits.Hits[0].Highlight["body.raw"])
}

// TestSearchV2_HighlightPerIndex checks that each hit is highlighted and its
// fields are formatted with the mappings of its own index, not of the index
// the mappings came from.
func TestSearchV2_HighlightPerIndex(t *testing.T) {
	indexName := "TestSearchV2_HighlightPerIndex.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	index.GetMappings().SetProperty("body", meta.Property{Type: "text", Index: true, Analyzer: "keyword"})
	index.GetMappings().SetProperty("day", meta.Property{Type: "date", Index: true, Format: "2006-01-02"})

	w, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	defer func() { assert.NoError(t, w.Close()) }()
	batch := riot.NewBatch()
	doc := riot.NewDocument("1").
		AddField(riot.NewTextField("body", "BrownFox").WithAnalyzer(analyzer.NewKeywordAnalyzer()).SearchTermPositions()).
		AddField(riot.NewStoredOnlyField("_source", []byte(`{"body":"BrownFox","day":"2026-01-02"}`))).
		AddField(riot.NewStoredOnlyField("_index", []byte(indexName)))
	batch.Update(doc.ID(), doc)
	require.NoError(t, w.Batch(batch))

	r, err := w.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, r.Close()) }()
	req := riot.NewTopNSearch(10, riot.NewTermQuery("BrownFox").SetField("body")).IncludeLocations().WithStandardAggregations()
	dmi, err := r.Search(context.Background(), req)
	require.NoError(t, err)

	// mappings of another index, where body uses the standard analyzer and day is not a date
	other := meta.NewMappings()
	other.SetProperty("body", meta.Property{Type: "text", Index: true})
	query := &meta.ZedQuery{Source: &meta.Source{Enable: true}, Highlight: &meta.Highlight{
		PreTags: []string{"<b>"}, PostTags: []string{"</b>"}, Fields: map[string]*meta.Highlight{"body": {NumberOfFragments: 1}},
	}, Fields: []*meta.Field{{Field: "day", Format: "02/01/2006"}}}
	resp, err := searchV2(1, 1, dmi, query, other, nil, map[string]*Index{indexName: index})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, []string{"<b>BrownFox</b>"}, resp.Hits.Hits[0].Highlight["body"])
	assert.Equal(t, []interface{}{"02/01/2026"}, resp.Hits.Hits[0].Fields["day"])
}

func TestIndex_Search_TopHitsRestoresVectors(t *testing.T) {
	indexName := "TestIndex_Search_TopHitsRestoresVectors.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	exclude := true
	require.NoError(t, index.SetSettings(&meta.IndexSettings{ExcludeSourceVectors: &exclude}))
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	index.GetMappings().SetProperty("emb", meta.NewProperty("vector"))
	index.GetMappings().SetProperty("name", meta.NewProperty("keyword"))

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "x", "emb": []interface{}{1.0, 2.0}}, false))
	waitForDocs(t, index, 1)

	resp, err := index.Search(&meta.ZedQuery{
		Query:        &meta.Query{MatchAll: &meta.MatchAllQuery{}},
		Size:         10,
		Aggregations: map[string]meta.Aggregations{"top": {TopHits: &meta.AggregationTopHits{Size: 1}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	assert.Equal(t, []interface{}{1.0, 2.0}, resp.Hits.Hits[0].Source.(map[string]interface{})["emb"])
	top := resp.Aggregations["top"].Hits
	require.NotNil(t, top)
	require.Len(t, top.Hits, 1)
	assert.Equal(t, []interface{}{1.0, 2.0}, top.Hits[0].Source.(map[string]interface{})["emb"])
}

// "_source": false skips restoring the excluded vectors; fields still read them.
func TestIndex_Search_SourceFalseSkipsVectors(t *testing.T) {
	indexName := "TestIndex_Search_SourceFalseSkipsVectors.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	exclude := true
	require.NoError(t, index.SetSettings(&meta.IndexSettings{ExcludeSourceVectors: &exclude}))
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	index.GetMappings().SetProperty("emb", meta.NewProperty("vector"))
	index.GetMappings().SetProperty("name", meta.NewProperty("keyword"))

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "x", "emb": []interface{}{1.0, 2.0}}, false))
	waitForDocs(t, index, 1)

	tests := []struct {
		name       string
		fields     interface{}
		wantFields map[string]interface{}
	}{
		{name: "no fields"},
		{name: "fields", fields: []interface{}{"emb"},
			wantFields: map[string]interface{}{"emb": []interface{}{[]interface{}{1.0, 2.0}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := index.Search(&meta.ZedQuery{
				Query:  &meta.Query{MatchAll: &meta.MatchAllQuery{}},
				Size:   10,
				Source: false,
				Fields: tt.fields,
			})
			require.NoError(t, err)
			require.Len(t, resp.Hits.Hits, 1)
			assert.NotContains(t, resp.Hits.Hits[0].Source, "emb")
			assert.NotContains(t, resp.Hits.Hits[0].Source, "name")
			if tt.wantFields == nil {
				assert.Nil(t, resp.Hits.Hits[0].Fields)
				return
			}
			assert.Equal(t, tt.wantFields["emb"], resp.Hits.Hits[0].Fields["emb"])
		})
	}
}

func TestIndex_Search_SourceDisabled(t *testing.T) {
	indexName := "TestIndex_Search_SourceDisabled.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()

	mappings := meta.NewMappings()
	mappings.SetSourceEnabled(false)
	mappings.SetProperty("body", meta.Property{Type: "text", Index: true, Highlightable: true})
	require.NoError(t, index.SetMappings(mappings))
	assert.False(t, index.GetMappings().SourceEnabled())

	nested := meta.NewMappings()
	nested.SetProperty("items", meta.NewProperty("nested"))
	assert.Error(t, index.SetMappings(nested), "nested needs _source")

	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"body": "quick brown fox"}, false))
	waitForDocs(t, index, 1)

	resp, err := index.Search(&meta.ZedQuery{
		Query: &meta.Query{Match: map[string]*meta.MatchQuery{"body": {Query: "brown"}}},
		Size:  10,
		Highlight: &meta.Highlight{Fields: map[string]*meta.Highlight{
			"body": {PreTags: []string{"<b>"}, PostTags: []string{"</b>"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 1)
	hit := resp.Hits.Hits[0]
	assert.Equal(t, "1", hit.ID)
	assert.Nil(t, hit.Source)
	assert.Equal(t, []string{"quick <b>brown</b> fox"}, hit.Highlight["body"])

	got, err := index.GetDocument("1")
	require.NoError(t, err)
	assert.Nil(t, got.Source)
}
