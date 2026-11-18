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
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	segment "github.com/vcaesar/bluge_segment_api"
	"github.com/vcaesar/ice/vec"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/analysis/analyzer"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestIndex_Index(t *testing.T) {
	indexName := "TestIndex_Index.index_1"
	index, err := NewIndex(indexName, "disk", 2)
	assert.NoError(t, err)
	assert.NotNil(t, index)

	err = StoreIndex(index)
	assert.NoError(t, err)

	got, err := json.Marshal(index)
	assert.NoError(t, err)
	assert.NotNil(t, got)

	name := index.GetName()
	assert.Equal(t, indexName, name)

	settings := index.GetSettings()
	assert.NotNil(t, settings)

	mappings := index.GetMappings()
	assert.NotNil(t, mappings)

	analyzers := index.GetAnalyzers()
	assert.NotNil(t, analyzers)

	err = index.Reopen()
	assert.NoError(t, err)
}

func TestIndex_BuildBlugeDocumentFromJSON(t *testing.T) {
	var index *Index
	var err error
	indexName := "TestIndex_BuildBlugeDocumentFromJSON.index_1"

	type args struct {
		docID string
		doc   map[string]interface{}
	}
	tests := []struct {
		name    string
		args    args
		init    func()
		want    *riot.Document
		wantErr bool
	}{
		{
			name: "normal",
			args: args{
				docID: "1",
				doc: map[string]interface{}{
					"id":     "1",
					"name":   "test1",
					"age":    10,
					"length": 3.14,
					"dev":    true,
					"address": map[string]interface{}{
						"street": "447 Great Mall Dr",
						"city":   "Milpitas",
						"state":  "CA",
						"zip":    95035,
					},
					"tag1":       []interface{}{"tag1", "tag2"},
					"tag2":       []interface{}{3.14, 3.15},
					"tag3":       []interface{}{true, false},
					"@timestamp": time.Now().Format(time.RFC3339),
					"time":       time.Now().Format(time.RFC3339),
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "timestamp with epoch_millis",
			args: args{
				docID: "2",
				doc: map[string]interface{}{
					"id":         "2",
					"name":       "test1",
					"age":        10,
					"length":     3.14,
					"dev":        true,
					"@timestamp": float64(1652176732575),
					"time":       float64(1652176732575),
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "timestamp with format",
			args: args{
				docID: "2",
				doc: map[string]interface{}{
					"id":         "2",
					"name":       "test1",
					"age":        10,
					"length":     3.14,
					"dev":        true,
					"@timestamp": time.Now().Format("2006-01-02 15:04:05.000"),
					"time":       time.Now().Format("2006-01-02 15:04:05.000"),
				},
			},
			init: func() {
				index.GetMappings().SetProperty("@timestamp", meta.Property{
					Type:   "time",
					Index:  true,
					Format: "2006-01-02 15:04:05.000",
				})
				index.GetMappings().SetProperty("time", meta.Property{
					Type:   "time",
					Index:  true,
					Format: "2006-01-02 15:04:05.000",
				})
			},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "with analyzer",
			args: args{
				docID: "3",
				doc: map[string]interface{}{
					"id":     "3",
					"name":   "test",
					"age":    "10",
					"length": 3,
					"dev":    true,
				},
			},
			init: func() {
				index.GetMappings().SetProperty("id", meta.Property{
					Type:         "keyword",
					Index:        true,
					Aggregatable: true,
				})
				index.GetMappings().SetProperty("name", meta.Property{
					Type:          "text",
					Analyzer:      "analyzer_1",
					Index:         true,
					Highlightable: true,
				})
				index.analyzers["analyzer_1"] = analyzer.NewStandardAnalyzer()
			},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "type conflict text",
			args: args{
				docID: "4",
				doc: map[string]interface{}{
					"id":   "4",
					"name": 3,
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "type conflict numeric",
			args: args{
				docID: "4",
				doc: map[string]interface{}{
					"id":     "4",
					"name":   "test1",
					"age":    "10",
					"length": 3,
					"dev":    true,
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "keyword type float64",
			args: args{
				docID: "4",
				doc: map[string]interface{}{
					"id": 3.14,
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "keyword type int",
			args: args{
				docID: "4",
				doc: map[string]interface{}{
					"id": 3,
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "keyword type bool",
			args: args{
				docID: "4",
				doc: map[string]interface{}{
					"id": false,
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
		{
			name: "keyword type other",
			args: args{
				docID: "4",
				doc: map[string]interface{}{
					"id": []byte("foo"),
				},
			},
			init:    func() {},
			want:    &riot.Document{},
			wantErr: false,
		},
	}

	t.Run("prepare", func(t *testing.T) {
		index, err = NewIndex(indexName, "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)

		err = StoreIndex(index)
		assert.NoError(t, err)
		index.GetMappings().SetProperty("time", meta.NewProperty("date"))
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.init()
			shard := index.GetShardByDocID(tt.args.docID)
			assert.NotNil(t, shard)

			got, err := shard.CheckDocument(tt.args.docID, tt.args.doc, false, 0)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.Nil(t, err)
			assert.NotNil(t, got)

			var doc map[string]interface{}
			err = json.Unmarshal(got, &doc)
			assert.NoError(t, err)
			assert.NotNil(t, doc)
			got2, err := shard.BuildBlugeDocumentFromJSON(tt.args.docID, doc)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.Nil(t, err)
			assert.NotNil(t, got2)
			wantType := reflect.TypeOf(tt.want)
			gotType := reflect.TypeOf(got2)
			assert.Equal(t, wantType, gotType)
		})
	}

	t.Run("cleanup", func(t *testing.T) {
		err := DeleteIndex(indexName)
		assert.NoError(t, err)
	})
}

func TestIndex_BuildBlugeDocumentFromJSON_GeoPoint(t *testing.T) {
	indexName := "TestIndex_BuildBlugeDocumentFromJSON_GeoPoint.index_1"
	index, err := NewIndex(indexName, "disk", 2)
	assert.NoError(t, err)
	assert.NoError(t, StoreIndex(index))
	index.GetMappings().SetProperty("location", meta.NewProperty("geo_point"))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()

	tests := []struct {
		name    string
		value   interface{}
		wantErr bool
	}{
		{name: "object", value: map[string]interface{}{"lat": 41.12, "lon": -71.34}},
		{name: "geojson", value: map[string]interface{}{"type": "Point", "coordinates": []interface{}{-71.34, 41.12}}},
		{name: "array", value: []interface{}{-71.34, 41.12}},
		{name: "string", value: "41.12,-71.34"},
		{name: "geohash", value: "drm3btev3e86"},
		{name: "invalid", value: "not a point", wantErr: true},
		{name: "out of range", value: []interface{}{-71.34, 100.0}, wantErr: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docID := strconv.Itoa(i)
			shard := index.GetShardByDocID(docID)
			got, err := shard.CheckDocument(docID, map[string]interface{}{"location": tt.value}, false, 0)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			var doc map[string]interface{}
			assert.NoError(t, json.Unmarshal(got, &doc))
			bdoc, err := shard.BuildBlugeDocumentFromJSON(docID, doc)
			assert.NoError(t, err)

			found := false
			bdoc.EachField(func(f segment.Field) {
				if f.Name() != "location" {
					return
				}
				found = true
				lon, lat, err := riot.DecodeGeoLonLat(f.Value())
				assert.NoError(t, err)
				assert.InDelta(t, -71.34, lon, 0.0001)
				assert.InDelta(t, 41.12, lat, 0.0001)
			})
			assert.True(t, found, "geo field not indexed")
		})
	}
}

func TestIndexShard_checkProperty_KeepsMappedText(t *testing.T) {
	indexName := "TestIndexShard_checkProperty_KeepsMappedText.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	assert.NoError(t, err)
	assert.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	defer func(v bool) { config.Global.EnableTextKeywordMapping = v }(config.Global.EnableTextKeywordMapping)

	for _, keyword := range []bool{false, true} {
		t.Run(strconv.FormatBool(keyword), func(t *testing.T) {
			config.Global.EnableTextKeywordMapping = keyword
			mappings := meta.NewMappings()
			mappings.SetProperty("body", meta.Property{Type: "text", Index: true, Highlightable: true, Analyzer: "simple"})
			shard := index.GetShardByDocID("1")

			assert.Equal(t, keyword, shard.checkProperty(mappings, "body", "quick fox"))
			body, _ := mappings.GetProperty("body")
			assert.True(t, body.Highlightable)
			assert.Equal(t, "simple", body.Analyzer)
			_, sub := mappings.GetProperty("body.keyword")
			assert.Equal(t, keyword, sub)
			_, field := body.Fields["keyword"]
			assert.Equal(t, keyword, field)
			assert.False(t, shard.checkProperty(mappings, "body", "quick fox"), "already mapped")
		})
	}
}

func TestIndex_BuildBlugeDocumentFromJSON_AllFieldPositions(t *testing.T) {
	indexName := "TestIndex_BuildBlugeDocumentFromJSON_AllFieldPositions.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	assert.NoError(t, err)
	assert.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	defer func(v bool) { config.Global.AllFieldPositions = v }(config.Global.AllFieldPositions)
	// .keyword sub-fields (sh/test.sh enables them) would add whole values to _all
	defer func(v bool) { config.Global.EnableTextKeywordMapping = v }(config.Global.EnableTextKeywordMapping)
	config.Global.EnableTextKeywordMapping = false
	index.GetMappings().SetAllEnabled(true)

	tests := []struct {
		name       string
		positions  bool
		phraseHits uint64
	}{
		{name: "positions", positions: true, phraseHits: 1},
		{name: "terms only", positions: false, phraseHits: 0},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.Global.AllFieldPositions = tt.positions
			docID := strconv.Itoa(i)
			shard := index.GetShardByDocID(docID)
			got, err := shard.CheckDocument(docID, map[string]interface{}{"title": "quick fox", "body": "quick brown fox"}, false, 0)
			assert.NoError(t, err)
			var doc map[string]interface{}
			assert.NoError(t, json.Unmarshal(got, &doc))
			bdoc, err := shard.BuildBlugeDocumentFromJSON(docID, doc)
			assert.NoError(t, err)

			bdoc.Analyze()
			found := false
			bdoc.EachField(func(f segment.Field) {
				if f.Name() != "_all" {
					return
				}
				found = true
				assert.Equal(t, 5, f.Length())
				f.EachTerm(func(term segment.FieldTerm) {
					want := map[string]int{"quick": 2, "fox": 2, "brown": 1}[string(term.Term())]
					assert.Equal(t, want, term.Frequency(), string(term.Term()))
					locs := 0
					term.EachLocation(func(segment.Location) { locs++ })
					if tt.positions {
						assert.Equal(t, want, locs)
					} else {
						assert.Zero(t, locs)
					}
				})
			})
			assert.True(t, found, "_all field not indexed")

			w, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
			assert.NoError(t, err)
			defer func() { assert.NoError(t, w.Close()) }()
			// Insert analyzes again, _all would count the terms above twice
			bdoc.ReleaseAnalysis()
			assert.NoError(t, w.Insert(bdoc))
			r, err := w.Reader()
			assert.NoError(t, err)
			defer func() { assert.NoError(t, r.Close()) }()
			count := func(q riot.Query) uint64 {
				dmi, err := r.Search(context.Background(), riot.NewTopNSearch(10, q).WithStandardAggregations())
				assert.NoError(t, err)
				return dmi.Aggregations().Count()
			}
			assert.Equal(t, uint64(1), count(riot.NewTermQuery("brown").SetField("_all")))
			assert.Equal(t, tt.phraseHits, count(riot.NewMatchPhraseQuery("brown fox").SetField("_all")))
			assert.Equal(t, uint64(1), count(riot.NewMatchPhraseQuery("brown fox").SetField("body")))
		})
	}
}

func TestIndex_BuildBlugeDocumentFromJSON_Vector(t *testing.T) {
	indexName := "TestIndex_BuildBlugeDocumentFromJSON_Vector.index_1"
	index, err := NewIndex(indexName, "disk", 2)
	assert.NoError(t, err)
	assert.NoError(t, StoreIndex(index))
	prop := meta.NewProperty("vector")
	prop.Dims = 3
	index.GetMappings().SetProperty("embedding", prop)
	index.GetMappings().SetProperty("any", meta.NewProperty("vector"))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()

	tests := []struct {
		name    string
		field   string
		value   interface{}
		want    []float32
		wantErr bool
	}{
		{name: "floats", field: "embedding", value: []interface{}{1.0, 2.0, 3.0}, want: []float32{1, 2, 3}},
		{name: "ints", field: "embedding", value: []interface{}{1, 2, 3}, want: []float32{1, 2, 3}},
		{name: "any dims", field: "any", value: []interface{}{0.5, -0.5}, want: []float32{0.5, -0.5}},
		{name: "wrong dims", field: "embedding", value: []interface{}{1.0, 2.0}, wantErr: true},
		{name: "not numeric", field: "embedding", value: []interface{}{"a", "b", "c"}, wantErr: true},
		{name: "scalar", field: "embedding", value: 1.0, wantErr: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docID := strconv.Itoa(i)
			shard := index.GetShardByDocID(docID)
			got, err := shard.CheckDocument(docID, map[string]interface{}{tt.field: tt.value}, false, 0)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			var doc map[string]interface{}
			assert.NoError(t, json.Unmarshal(got, &doc))
			bdoc, err := shard.BuildBlugeDocumentFromJSON(docID, doc)
			assert.NoError(t, err)

			count := 0
			bdoc.EachField(func(f segment.Field) {
				if f.Name() != tt.field {
					return
				}
				count++
				decoded, err := vec.Decode(f.Value())
				assert.NoError(t, err)
				assert.Equal(t, tt.want, decoded)
			})
			assert.Equal(t, 1, count, "vector must be indexed as a single field")
		})
	}
}

func TestIndex_Settings(t *testing.T) {
	var index *Index
	var err error
	indexName := "TestIndex_Settings.index_1"

	t.Run("prepare", func(t *testing.T) {
		index, err = NewIndex(indexName, "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)

		err = StoreIndex(index)
		assert.NoError(t, err)
	})

	t.Run("setting", func(t *testing.T) {
		err := index.SetSettings(&meta.IndexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
			Analysis: &meta.IndexAnalysis{
				Analyzer: map[string]*meta.Analyzer{
					"default": {
						Type: "standard",
					},
				},
			},
		})
		assert.NoError(t, err)
	})

	t.Run("tier", func(t *testing.T) {
		assert.NoError(t, index.SetSettings(&meta.IndexSettings{Tier: TierHot}))
		assert.Error(t, index.SetSettings(&meta.IndexSettings{Tier: "frozen"}))
		assert.Error(t, index.SetSettings(&meta.IndexSettings{Tier: TierCold}))
		assert.Equal(t, TierHot, index.GetTier())
		assert.False(t, index.IsReadOnly())
	})

	t.Run("mapping", func(t *testing.T) {
		err := index.SetMappings(&meta.Mappings{
			Properties: map[string]meta.Property{
				"id": meta.NewProperty("keyword"),
			},
		})
		assert.NoError(t, err)
	})

	t.Run("indexed vector mapping is kept", func(t *testing.T) {
		require.NoError(t, index.SetMappings(&meta.Mappings{
			Properties: map[string]meta.Property{"emb": meta.NewProperty("vector")},
		}))
		unindexed := meta.NewProperty("vector")
		unindexed.Index = false
		for name, prop := range map[string]meta.Property{"disable index": unindexed, "change type": meta.NewProperty("text")} {
			t.Run(name, func(t *testing.T) {
				assert.Error(t, index.SetMappings(&meta.Mappings{Properties: map[string]meta.Property{"emb": prop}}))
				assert.Equal(t, []string{"emb"}, index.GetMappings().VectorFields())
			})
		}
		assert.NoError(t, index.SetMappings(&meta.Mappings{
			Properties: map[string]meta.Property{"emb": meta.NewProperty("vector")},
		}), "an unchanged vector mapping is accepted")
	})

	t.Run("analyzer", func(t *testing.T) {
		err := index.SetAnalyzers(map[string]*analysis.Analyzer{
			"standard": analyzer.NewStandardAnalyzer(),
		})
		assert.NoError(t, err)
	})

	t.Run("cleanup", func(t *testing.T) {
		err := DeleteIndex(indexName)
		assert.NoError(t, err)
	})
}

func TestIndex_BuildBlugeDocumentFromJSON_ExcludeSourceVectors(t *testing.T) {
	for _, exclude := range []bool{true, false} {
		t.Run(strconv.FormatBool(exclude), func(t *testing.T) {
			indexName := "TestIndex_BuildBlugeDocumentFromJSON_ExcludeSourceVectors.index_" + strconv.FormatBool(exclude)
			index, err := NewIndex(indexName, "disk", 1)
			assert.NoError(t, err)
			assert.NoError(t, index.SetSettings(&meta.IndexSettings{ExcludeSourceVectors: &exclude}))
			assert.NoError(t, StoreIndex(index))
			defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
			index.GetMappings().SetProperty("emb", meta.NewProperty("vector"))
			index.GetMappings().SetProperty("name", meta.NewProperty("keyword"))
			assert.Equal(t, exclude, index.ExcludeSourceVectors())

			shard := index.GetShardByDocID("1")
			got, err := shard.CheckDocument("1", map[string]interface{}{"name": "x", "emb": []interface{}{1.0, 2.0}}, false, 0)
			assert.NoError(t, err)
			var doc map[string]interface{}
			assert.NoError(t, json.Unmarshal(got, &doc))
			bdoc, err := shard.BuildBlugeDocumentFromJSON("1", doc)
			assert.NoError(t, err)

			stored := map[string]string{}
			bdoc.EachField(func(f segment.Field) {
				if f.Name() == "_source" || f.Name() == meta.SourceVectorsField {
					stored[f.Name()] = string(f.Value())
				}
			})
			if exclude {
				assert.JSONEq(t, `{"name":"x"}`, stored["_source"])
				assert.JSONEq(t, `[["emb"]]`, stored[meta.SourceVectorsField])
			} else {
				assert.JSONEq(t, `{"name":"x","emb":[1,2]}`, stored["_source"])
				assert.NotContains(t, stored, meta.SourceVectorsField)
			}
		})
	}
}

// A document field named like the marker is not indexed: its stored value
// would be read back as the list of excluded vectors.
func TestIndex_BuildBlugeDocumentFromJSON_ReservedSourceVectorsField(t *testing.T) {
	indexName := "TestIndex_BuildBlugeDocumentFromJSON_ReservedSourceVectorsField.index_1"
	index, err := NewIndex(indexName, "disk", 1)
	require.NoError(t, err)
	exclude := true
	require.NoError(t, index.SetSettings(&meta.IndexSettings{ExcludeSourceVectors: &exclude}))
	require.NoError(t, StoreIndex(index))
	defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
	index.GetMappings().SetProperty(meta.SourceVectorsField, meta.NewProperty("vector"))
	index.GetMappings().SetProperty("emb", meta.NewProperty("vector"))

	shard := index.GetShardByDocID("1")
	got, err := shard.CheckDocument("1", map[string]interface{}{
		meta.SourceVectorsField: []interface{}{3.0, 4.0}, "emb": []interface{}{1.0, 2.0},
	}, false, 0)
	require.NoError(t, err)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(got, &doc))
	bdoc, err := shard.BuildBlugeDocumentFromJSON("1", doc)
	require.NoError(t, err)

	stored := map[string][]string{}
	bdoc.EachField(func(f segment.Field) {
		if f.Name() == "_source" || f.Name() == meta.SourceVectorsField {
			stored[f.Name()] = append(stored[f.Name()], string(f.Value()))
		}
	})
	require.Len(t, stored[meta.SourceVectorsField], 1, "only the marker is stored under the reserved name")
	assert.JSONEq(t, `[["emb"]]`, stored[meta.SourceVectorsField][0])
	require.Len(t, stored["_source"], 1)
	assert.JSONEq(t, `{"_source_vectors":[3,4]}`, stored["_source"][0])
}

// The first value set wins, whichever it is: later settings do not change it.
func TestIndex_SetSettings_ExcludeSourceVectorsImmutable(t *testing.T) {
	for _, first := range []bool{true, false} {
		t.Run(strconv.FormatBool(first), func(t *testing.T) {
			indexName := "TestIndex_SetSettings_ExcludeSourceVectorsImmutable.index_" + strconv.FormatBool(first)
			index, err := NewIndex(indexName, "disk", 1)
			require.NoError(t, err)
			require.NoError(t, StoreIndex(index))
			defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
			assert.Equal(t, config.Global.ExcludeSourceVectors, index.ExcludeSourceVectors(), "inherited until set")

			other := !first
			require.NoError(t, index.SetSettings(&meta.IndexSettings{ExcludeSourceVectors: &first}))
			assert.Equal(t, first, index.ExcludeSourceVectors())
			require.NoError(t, index.SetSettings(&meta.IndexSettings{ExcludeSourceVectors: &other}))
			assert.Equal(t, first, index.ExcludeSourceVectors())
			require.NoError(t, index.SetSettings(&meta.IndexSettings{NumberOfReplicas: 1}))
			assert.Equal(t, first, index.ExcludeSourceVectors())
		})
	}
}

func TestIndex_BuildBlugeDocumentFromJSON_OptionalFields(t *testing.T) {
	defer func(v bool) { config.Global.AutoTimestamp = v }(config.Global.AutoTimestamp)
	tests := []struct {
		name          string
		all           bool
		autoTimestamp bool
		doc           map[string]interface{}
		wantAll       bool
		wantTimestamp bool
	}{
		{name: "defaults off", autoTimestamp: true, doc: map[string]interface{}{"body": "fox"}, wantTimestamp: true},
		{name: "_all on", all: true, autoTimestamp: true, doc: map[string]interface{}{"body": "fox"}, wantAll: true, wantTimestamp: true},
		{name: "no auto timestamp", doc: map[string]interface{}{"body": "fox"}},
		{name: "no auto timestamp keeps own", doc: map[string]interface{}{"body": "fox", "@timestamp": "2026-01-02T03:04:05Z"}, wantTimestamp: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexName := "TestIndex_BuildBlugeDocumentFromJSON_OptionalFields.index_" + strconv.Itoa(i)
			index, err := NewIndex(indexName, "disk", 1)
			assert.NoError(t, err)
			assert.NoError(t, StoreIndex(index))
			defer func() { assert.NoError(t, DeleteIndex(indexName)) }()
			index.GetMappings().SetAllEnabled(tt.all) // not left to zed_all_field
			config.Global.AutoTimestamp = tt.autoTimestamp

			shard := index.GetShardByDocID("1")
			got, err := shard.CheckDocument("1", tt.doc, false, 0)
			assert.NoError(t, err)
			var doc map[string]interface{}
			assert.NoError(t, json.Unmarshal(got, &doc))
			bdoc, err := shard.BuildBlugeDocumentFromJSON("1", doc)
			assert.NoError(t, err)

			fields := map[string]bool{}
			bdoc.EachField(func(f segment.Field) { fields[f.Name()] = true })
			assert.Equal(t, tt.wantAll, fields["_all"])
			assert.Equal(t, tt.wantTimestamp, fields[meta.TimeFieldName])
			assert.True(t, fields["body"])
		})
	}
}
