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

func TestSearch_BadRescore(t *testing.T) {
	name := "TestSearch_BadRescore.index_1"
	index := newRescoreIndex(t, name)
	q := rescoreQuery("bad")

	_, err := index.Search(q)
	assert.ErrorContains(t, err, "[rescore] must be an object")
	_, err = MultiSearch([]string{name}, q)
	assert.ErrorContains(t, err, "[rescore] must be an object")
}

func TestSearch_FinishError(t *testing.T) {
	name := "TestSearch_FinishError.index_1"
	index := newRescoreIndex(t, name)
	q := &meta.ZedQuery{Size: 10, Suggest: map[string]interface{}{"s": "bad"}}

	_, err := index.Search(q)
	assert.Error(t, err)
	_, err = MultiSearch([]string{name}, q)
	assert.Error(t, err)
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
