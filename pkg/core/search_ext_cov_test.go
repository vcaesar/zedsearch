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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/uquery"
)

// failingCtx reports no error for its first ok Err calls and
// context.DeadlineExceeded afterwards.
type failingCtx struct {
	context.Context
	ok int
}

func (c *failingCtx) Err() error {
	if c.ok > 0 {
		c.ok--
		return nil
	}
	return context.DeadlineExceeded
}

// searchFailReader returns a reader of one document whose searches fail.
func searchFailReader(t *testing.T) *riot.Reader {
	t.Helper()
	w, err := riot.OpenWriter(riot.InMemoryOnlyConfig().WithSearchStartFunc(func(uint64) error { return assert.AnError }))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, w.Close()) })
	doc := riot.NewDocument("a")
	require.NoError(t, w.Update(doc.ID(), doc))
	r, err := w.Reader()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	return r
}

func TestSearchFailed(t *testing.T) {
	resp, err := searchFailed(context.DeadlineExceeded)
	require.NoError(t, err)
	assert.True(t, resp.TimedOut)
	assert.Equal(t, context.DeadlineExceeded.Error(), resp.Error)
	assert.Empty(t, resp.Hits.Hits)

	resp, err = searchFailed(assert.AnError)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, assert.AnError)
}

// searchFunc is a search entrypoint over one index.
type searchFunc func(*meta.ZedQuery) (*meta.SearchResponse, error)

// searchEntrypoints returns Search and MultiSearch over index by name.
func searchEntrypoints(index *Index) map[string]searchFunc {
	return map[string]searchFunc{
		"Search": index.Search,
		"MultiSearch": func(q *meta.ZedQuery) (*meta.SearchResponse, error) {
			return MultiSearch([]string{index.GetName()}, q)
		},
	}
}

func TestSearch_BadRescore(t *testing.T) {
	index := newRescoreIndex(t, "TestSearch_BadRescore.index_1")
	for name, search := range searchEntrypoints(index) {
		t.Run(name, func(t *testing.T) {
			_, err := search(rescoreQuery("bad"))
			assert.ErrorContains(t, err, "[rescore] must be an object")
		})
	}
}

func TestSearch_FinishError(t *testing.T) {
	index := newRescoreIndex(t, "TestSearch_FinishError.index_1")
	for name, search := range searchEntrypoints(index) {
		t.Run(name, func(t *testing.T) {
			_, err := search(&meta.ZedQuery{Size: 10, Suggest: map[string]interface{}{"s": "bad"}})
			assert.Error(t, err)
		})
	}
}

// TestSearch_PipelinesReadSignificantBuckets pins that pipeline aggregations
// read the buckets significance kept, not the candidates it dropped.
func TestSearch_PipelinesReadSignificantBuckets(t *testing.T) {
	name := "TestSearch_PipelinesReadSignificantBuckets.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	mappings := index.GetMappings()
	mappings.SetProperty("tag", meta.NewProperty("keyword"))
	require.NoError(t, index.SetMappings(mappings))
	// x is significant for fox, y is as frequent in it as in the index
	docs := map[string][2]string{
		"a": {"fox", "x"}, "b": {"fox", "x"}, "c": {"fox", "y"},
		"d": {"dog", "y"}, "e": {"dog", "z"}, "f": {"dog", "z"},
	}
	for id, doc := range docs {
		require.NoError(t, index.CreateDocument(id, map[string]interface{}{"title": doc[0], "tag": doc[1]}, false))
	}
	waitForDocs(t, index, len(docs))

	for name, search := range searchEntrypoints(index) {
		t.Run(name, func(t *testing.T) {
			minDocCount := 1
			resp, err := search(&meta.ZedQuery{
				Query: map[string]interface{}{"match": map[string]interface{}{"title": "fox"}},
				Aggregations: map[string]meta.Aggregations{
					"sig":   {SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag", MinDocCount: &minDocCount}},
					"total": {SumBucket: &meta.AggregationPipeline{BucketsPath: "sig>_count"}},
				},
			})
			require.NoError(t, err)
			buckets, _ := resp.Aggregations["sig"].Buckets.([]map[string]interface{})
			require.Len(t, buckets, 1)
			assert.Equal(t, "x", buckets[0]["key"])
			assert.EqualValues(t, 2, buckets[0]["doc_count"])
			assert.EqualValues(t, 2, resp.Aggregations["total"].Value)
		})
	}
}

func TestSearchExt_FinishErrors(t *testing.T) {
	hits := func() *meta.SearchResponse {
		return &meta.SearchResponse{Hits: meta.Hits{Hits: []meta.Hit{{ID: "a"}}}}
	}
	tests := []struct {
		name string
		ctx  context.Context
		ext  *searchExt
		resp *meta.SearchResponse
		err  error
	}{
		{
			name: "rescore query",
			ctx:  context.Background(),
			ext: &searchExt{query: &meta.ZedQuery{}, mappings: meta.NewMappings(), rescorers: []*uquery.Rescorer{
				{WindowSize: 1, Query: map[string]interface{}{"bogus": map[string]interface{}{}}},
			}},
			resp: hits(),
		},
		{
			name: "rescore search",
			ctx:  context.Background(),
			ext: &searchExt{query: &meta.ZedQuery{}, mappings: meta.NewMappings(), readers: []*riot.Reader{searchFailReader(t)}, rescorers: []*uquery.Rescorer{
				{WindowSize: 1, Query: map[string]interface{}{"match_all": map[string]interface{}{}}},
			}},
			resp: hits(),
		},
		{
			name: "suggest",
			ctx:  context.Background(),
			ext:  &searchExt{query: &meta.ZedQuery{Suggest: map[string]interface{}{"s": "bad"}}},
			resp: hits(),
		},
		{
			name: "significant terms",
			ctx:  context.Background(),
			ext: &searchExt{query: &meta.ZedQuery{Aggregations: map[string]meta.Aggregations{
				"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "tag", Include: "("}},
			}}},
			resp: &meta.SearchResponse{Aggregations: map[string]meta.AggregationResponse{"sig": {}}},
		},
		{
			name: "deadline after rescore",
			ctx:  &failingCtx{Context: context.Background(), ok: 1},
			ext:  &searchExt{query: &meta.ZedQuery{}, rescorers: []*uquery.Rescorer{{WindowSize: 1}}},
			resp: &meta.SearchResponse{},
			err:  context.DeadlineExceeded,
		},
		{
			name: "deadline after suggest",
			ctx:  &failingCtx{Context: context.Background(), ok: 2},
			ext:  &searchExt{query: &meta.ZedQuery{}},
			resp: &meta.SearchResponse{},
			err:  context.DeadlineExceeded,
		},
		{
			name: "deadline after significant terms",
			ctx:  &failingCtx{Context: context.Background(), ok: 3},
			ext:  &searchExt{query: &meta.ZedQuery{}},
			resp: &meta.SearchResponse{},
			err:  context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ext.finish(tt.ctx, tt.resp)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
				return
			}
			assert.Error(t, err)
		})
	}
}

// TestSearch_RuleDocsIndex pins that the docs of a rule action are pinned
// and excluded in their own index only.
func TestSearch_RuleDocsIndex(t *testing.T) {
	a, b := "TestSearch_RuleDocsIndex.index_a", "TestSearch_RuleDocsIndex.index_b"
	for _, name := range []string{a, b} {
		index, err := NewIndex(name, StorageTypeDisk, 1)
		require.NoError(t, err)
		require.NoError(t, StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
		for _, id := range []string{"1", "2"} {
			require.NoError(t, index.CreateDocument(id, map[string]interface{}{"title": "fox"}, false))
		}
		waitForDocs(t, index, 2)
	}
	always := []meta.QueryRuleCriteria{{Type: "always"}}
	set := "TestSearch_RuleDocsIndex.rules_1"
	require.NoError(t, metadata.QueryRules.Set(set, meta.QueryRuleset{ID: set, Rules: []*meta.QueryRule{
		{ID: "pin", Type: "pinned", Criteria: always, Actions: meta.QueryRuleActions{Docs: []meta.QueryRuleDocRef{{Index: b, ID: "2"}}}},
		{ID: "drop", Type: "exclude", Criteria: always, Actions: meta.QueryRuleActions{Docs: []meta.QueryRuleDocRef{{Index: a, ID: "1"}}}},
	}}))
	t.Cleanup(func() { assert.NoError(t, metadata.QueryRules.Delete(set)) })

	resp, err := MultiSearch([]string{a, b}, &meta.ZedQuery{Size: 10, Query: map[string]interface{}{"rule": map[string]interface{}{
		"ruleset_ids":    []interface{}{set},
		"match_criteria": map[string]interface{}{},
		"organic":        map[string]interface{}{"match": map[string]interface{}{"title": "fox"}},
	}}})
	require.NoError(t, err)
	hits := make([]string, len(resp.Hits.Hits))
	for i, h := range resp.Hits.Hits {
		hits[i] = h.Index + "/" + h.ID
	}
	require.Len(t, hits, 3)
	assert.Equal(t, b+"/2", hits[0])
	assert.ElementsMatch(t, []string{a + "/2", b + "/1"}, hits[1:])
	assert.Less(t, resp.Hits.Hits[1].Score, resp.Hits.Hits[0].Score/2, "the same id of the other index is not pinned")
}

// TestPrepareSearch_HonorsDeadline pins that the more_like_this rewrite stops
// with the search deadline.
func TestPrepareSearch_HonorsDeadline(t *testing.T) {
	index := newRescoreIndex(t, "TestPrepareSearch_HonorsDeadline.index_1")
	readers, err := index.GetReaders(0, 0)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, r := range readers {
			assert.NoError(t, r.Close())
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	q := &meta.ZedQuery{Query: map[string]interface{}{"more_like_this": map[string]interface{}{
		"like": "fox", "fields": []interface{}{"title"}, "min_term_freq": 1, "min_doc_freq": 1,
	}}}
	_, err = prepareSearch(ctx, q, index.GetMappings(), index.GetAnalyzers(), readers)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestSearch_RescoreTransforms pins that rule and more_like_this clauses of a
// rescore query are rewritten like the main query.
func TestSearch_RescoreTransforms(t *testing.T) {
	index := newRescoreIndex(t, "TestSearch_RescoreTransforms.index_1")
	mlt := map[string]interface{}{"more_like_this": map[string]interface{}{
		"like": "fox", "fields": []interface{}{"title"}, "min_term_freq": 1, "min_doc_freq": 1,
	}}

	resp, err := index.Search(rescoreQuery(map[string]interface{}{
		"query": map[string]interface{}{"rescore_query": mlt},
	}))
	require.NoError(t, err)
	assert.Len(t, resp.Hits.Hits, 2)

	_, err = index.Search(rescoreQuery(map[string]interface{}{
		"query": map[string]interface{}{"rescore_query": map[string]interface{}{"rule": map[string]interface{}{
			"ruleset_ids":    []interface{}{"TestSearch_RescoreTransforms.missing"},
			"match_criteria": map[string]interface{}{},
			"organic":        mlt,
		}}},
	}))
	assert.ErrorContains(t, err, "[rule] ruleset [TestSearch_RescoreTransforms.missing] not found")
}

// TestSearchExt_FinishPipelineError pins that a failing pipeline aggregation
// is logged and leaves the search answered, as before pipelines moved here.
func TestSearchExt_FinishPipelineError(t *testing.T) {
	ext := &searchExt{query: &meta.ZedQuery{Aggregations: map[string]meta.Aggregations{
		"avg_sales": {AvgBucket: &meta.AggregationPipeline{BucketsPath: "missing>sales"}},
	}}}
	resp := &meta.SearchResponse{Aggregations: map[string]meta.AggregationResponse{}}
	require.NoError(t, ext.finish(context.Background(), resp))
	assert.Empty(t, resp.Aggregations)
}

func TestMultiSearch_SuggestWithoutShardReaders(t *testing.T) {
	name := "TestMultiSearch_SuggestWithoutShardReaders.index_1"
	// a cached index without shards has no readers
	ZED_INDEX_LIST.Add(&Index{ref: &meta.Index{Name: name, Mappings: meta.NewMappings()}})
	t.Cleanup(func() {
		ZED_INDEX_LIST.lock.Lock()
		delete(ZED_INDEX_LIST.Indexes, name)
		ZED_INDEX_LIST.lock.Unlock()
	})

	tests := []struct {
		name    string
		suggest map[string]interface{}
		wantErr bool
	}{
		{name: "named entry", suggest: map[string]interface{}{"s": map[string]interface{}{"text": "fox", "term": map[string]interface{}{"field": "title"}}}},
		{name: "malformed", suggest: map[string]interface{}{"s": "bad"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := MultiSearch([]string{name}, &meta.ZedQuery{Size: 10, Suggest: tt.suggest})
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, resp.Suggest, "s")
			assert.Empty(t, resp.Hits.Hits)
		})
	}
}

func TestScoreSorted_MultiKeyObject(t *testing.T) {
	assert.False(t, scoreSorted(map[string]interface{}{"_score": "desc", "year": "desc"}))
}

func TestCheckSynonymSets_NoSettings(t *testing.T) {
	assert.NoError(t, checkSynonymSets(&Index{ref: &meta.Index{}}))
}

func TestDeleteSynonymSet_ListError(t *testing.T) {
	name := "TestDeleteSynonymSet_ListError.index_1"
	require.NoError(t, metadata.Index.Set(name, []byte("not json")))
	t.Cleanup(func() { assert.NoError(t, metadata.Index.Delete(name)) })

	used, err := DeleteSynonymSet("TestDeleteSynonymSet_ListError.set_1")
	assert.Error(t, err)
	assert.Nil(t, used)
}
