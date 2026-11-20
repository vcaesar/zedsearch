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

package aggregation

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	zedaggregation "github.com/vcaesar/zedsearch/pkg/bluge/aggregation"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestRequestSignificantTerms(t *testing.T) {
	mappings := meta.NewMappings()
	for field, typ := range map[string]string{"body": "text", "tag": "keyword", "n": "numeric", "ok": "bool", "ts": "date"} {
		mappings.SetProperty(field, meta.NewProperty(typ))
	}
	aggText := meta.NewProperty("text")
	aggText.Aggregatable = true
	mappings.SetProperty("agg_body", aggText)
	sub := map[string]meta.Aggregations{"max_n": {Max: &meta.AggregationMetric{Field: "n"}}}
	countSub := map[string]meta.Aggregations{"count": {Max: &meta.AggregationMetric{Field: "n"}}}
	tests := []struct {
		name    string
		agg     meta.Aggregations
		wantErr string
	}{
		{name: "keyword", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}}},
		{name: "numeric with sub aggregations", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "n", Size: 50}, Aggregations: sub}},
		{name: "bool", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "ok"}}},
		{name: "significant_text", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "agg_body"}}},
		{name: "significant_text on default text", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "body"}},
			wantErr: "[significant_text] field [body] of type [text] must be mapped with aggregatable: true"},
		{name: "sub aggregation named score", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}, Aggregations: map[string]meta.Aggregations{"score": {Max: &meta.AggregationMetric{Field: "n"}}}},
			wantErr: "[significant_terms] sub aggregation name [score] is reserved"},
		{name: "sub aggregation named bg_count", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}, Aggregations: map[string]meta.Aggregations{"bg_count": {Max: &meta.AggregationMetric{Field: "n"}}}},
			wantErr: "[significant_terms] sub aggregation name [bg_count] is reserved"},
		{name: "unsupported type", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "ts"}},
			wantErr: "[significant_terms] aggregation doesn't support values of type: [ts:[date]]"},
		{name: "significant_text unmapped", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "nope"}},
			wantErr: "[significant_text] aggregation doesn't support values of type"},
		{name: "bad sub aggregation", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}, Aggregations: globalSub},
			wantErr: "[global]"},
		{name: "sub aggregation named count", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag"}, Aggregations: countSub},
			wantErr: "[significant_terms] sub aggregation name [count] is reserved"},
		{name: "source_fields", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "body", SourceFields: []string{"body"}}},
			wantErr: "[significant_text] source_fields is not supported"},
		{name: "filter_duplicate_text", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "body", FilterDuplicateText: true}},
			wantErr: "[significant_text] filter_duplicate_text is not supported"},
		{name: "overflowing size", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag", Size: 1 << 61}},
			wantErr: "[significant_terms] size must be in [0, 10000]"},
		{name: "negative size", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag", Size: -1}},
			wantErr: "[significant_terms] size must be in [0, 10000]"},
		{name: "shard_size above limit", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "agg_body", ShardSize: meta.MaxSignificantTermsSize + 1}},
			wantErr: "[significant_text] shard_size must be in [0, 10000]"},
		{name: "size at limit", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag", Size: meta.MaxSignificantTermsSize}}},
		{name: "include list and exclude regexp", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag",
			Include: []interface{}{"a", 1}, Exclude: "b.*"}}},
		{name: "bad include regexp", agg: meta.Aggregations{SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag", Include: "["}},
			wantErr: "[significant_terms] invalid include regexp [[]"},
		{name: "unsupported exclude", agg: meta.Aggregations{SignificantText: &meta.AggregationSignificantTerms{Field: "agg_body", Exclude: map[string]interface{}{"partition": 0}}},
			wantErr: "[significant_text] exclude doesn't support values of type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newTestSearchAggregation()
			err := Request(req, map[string]meta.Aggregations{"sig": tt.agg}, mappings)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.IsType(t, &zedaggregation.TermsAggregation{}, req.aggs["sig"])
		})
	}
}

func TestSignificantTermsSizes(t *testing.T) {
	tests := []struct {
		name              string
		size, shardSize   int
		wantReq, wantResp int
	}{
		{"defaults", 0, 0, 100, 10},
		{"size times five", 30, 0, 150, 30},
		{"shard_size wins", 5, 500, 500, 5},
		{"size at limit", meta.MaxSignificantTermsSize, 0, 5 * meta.MaxSignificantTermsSize, meta.MaxSignificantTermsSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &meta.AggregationSignificantTerms{Size: tt.size, ShardSize: tt.shardSize}
			assert.Equal(t, tt.wantReq, a.RequestSize())
			assert.Equal(t, tt.wantResp, a.ResponseSize())
		})
	}
}

// sigDoc is indexed as the document writer does: keyword values, one field
// per value, and bool values as "true" / "false" keywords.
type sigDoc struct {
	tags []string
	ok   bool
}

// sigBuckets runs aggs over docs in an in-memory index and returns the
// doc_count by key of the collected foreground buckets of "sig".
func sigBuckets(t *testing.T, docs []sigDoc, sig *meta.AggregationSignificantTerms) map[string]uint64 {
	t.Helper()
	writer, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, writer.Close()) })
	batch := riot.NewBatch()
	for i, d := range docs {
		id := strconv.Itoa(i)
		doc := riot.NewDocument(id).AddField(riot.NewKeywordField("ok", strconv.FormatBool(d.ok)).Aggregatable())
		for _, tag := range d.tags {
			doc.AddField(riot.NewKeywordField("tag", tag).Aggregatable())
		}
		batch.Update(riot.Identifier(id), doc)
	}
	require.NoError(t, writer.Batch(batch))
	reader, err := writer.Reader()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reader.Close()) })

	mappings := meta.NewMappings()
	mappings.SetProperty("tag", meta.NewProperty("keyword"))
	mappings.SetProperty("ok", meta.NewProperty("bool"))
	req := riot.NewTopNSearch(0, riot.NewMatchAllQuery()).WithStandardAggregations()
	require.NoError(t, Request(req, map[string]meta.Aggregations{"sig": {SignificantTerms: sig}}, mappings))
	dmi, err := reader.Search(context.Background(), req)
	require.NoError(t, err)
	resp, err := Response(dmi.Aggregations())
	require.NoError(t, err)
	buckets, ok := resp["sig"].Buckets.([]map[string]interface{})
	require.True(t, ok, "%T", resp["sig"].Buckets)
	rv := make(map[string]uint64, len(buckets))
	for _, b := range buckets {
		key := fmt.Sprint(b["key"])
		rv[key] = b["doc_count"].(uint64)
	}
	return rv
}

func TestSignificantTermsForeground(t *testing.T) {
	docs := []sigDoc{{tags: []string{"a", "b", "a"}, ok: true}, {tags: []string{"b"}, ok: true}, {tags: []string{"c"}}}
	// 101 terms in two docs each push a single-doc term out of the 100 candidates
	crowded := []sigDoc{{tags: []string{"rare"}}}
	for i := range 101 {
		tag := fmt.Sprintf("t%03d", i)
		crowded = append(crowded, sigDoc{tags: []string{tag}}, sigDoc{tags: []string{tag}})
	}
	tests := []struct {
		name string
		docs []sigDoc
		sig  *meta.AggregationSignificantTerms
		want map[string]uint64
	}{
		{name: "multi-valued keyword counts each distinct value", docs: docs,
			sig: &meta.AggregationSignificantTerms{Field: "tag"}, want: map[string]uint64{"a": 1, "b": 2, "c": 1}},
		{name: "bool stored as keyword", docs: docs,
			sig: &meta.AggregationSignificantTerms{Field: "ok"}, want: map[string]uint64{"true": 2, "false": 1}},
		{name: "include list before candidate limit", docs: crowded,
			sig: &meta.AggregationSignificantTerms{Field: "tag", Include: []interface{}{"rare", "t000"}}, want: map[string]uint64{"rare": 1, "t000": 2}},
		{name: "include regexp before candidate limit", docs: crowded,
			sig: &meta.AggregationSignificantTerms{Field: "tag", Include: "r.*"}, want: map[string]uint64{"rare": 1}},
		{name: "exclude before candidate limit", docs: crowded,
			sig:  &meta.AggregationSignificantTerms{Field: "tag", Exclude: "t0.*"},
			want: map[string]uint64{"rare": 1, "t100": 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sigBuckets(t, tt.docs, tt.sig))
		})
	}
}
