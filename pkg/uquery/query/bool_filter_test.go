/* Copyright 2026 AtomAI. and Contributors
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

package query

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"
)

// scoreSpy records the options the filter clause passes to its searcher.
type scoreSpy struct {
	riot.Query
	got *search.SearcherOptions
}

func (s scoreSpy) Searcher(i search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	*s.got = options
	return s.Query.Searcher(i, options)
}

func filterTestReader(t *testing.T) *riot.Reader {
	t.Helper()
	writer, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, writer.Close()) })
	batch := riot.NewBatch()
	for id, doc := range map[string][2]string{
		"1": {"red apple", "a"},
		"2": {"green apple pie", "b"},
		"3": {"apple apple apple", "a"},
		"4": {"blue sky", "a"},
	} {
		batch.Update(riot.Identifier(id), riot.NewDocument(id).
			AddField(riot.NewTextField("name", doc[0])).
			AddField(riot.NewKeywordField("tag", doc[1])))
	}
	require.NoError(t, writer.Batch(batch))
	reader, err := writer.Reader()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reader.Close()) })
	return reader
}

func scores(t *testing.T, r *riot.Reader, req riot.SearchRequest) map[string]float64 {
	t.Helper()
	dmi, err := r.Search(context.Background(), req)
	require.NoError(t, err)
	rv := map[string]float64{}
	for m, err := dmi.Next(); m != nil || err != nil; m, err = dmi.Next() {
		require.NoError(t, err)
		require.NoError(t, m.VisitStoredFields(func(field string, value []byte) bool {
			if field == "_id" {
				rv[string(value)] = m.Score
			}
			return true
		}))
	}
	return rv
}

func TestBoolQuery_FilterSkipsScoring(t *testing.T) {
	r := filterTestReader(t)
	q, err := BoolQuery(map[string]interface{}{
		"must":   map[string]interface{}{"match": map[string]interface{}{"name": "apple"}},
		"filter": termOn("tag", "a"),
	}, testMappings(), nil)
	require.NoError(t, err)

	// same matches and scores as a plain boost-0 filter clause
	plain := riot.NewBooleanQuery().
		AddMust(riot.NewMatchQuery("apple").SetField("name")).
		AddMust(riot.NewBooleanQuery().SetBoost(0).AddMust(riot.NewTermQuery("a").SetField("tag")))
	got := scores(t, r, riot.NewTopNSearch(10, q))
	assert.Equal(t, scores(t, r, riot.NewTopNSearch(10, plain)), got)
	assert.Len(t, got, 2)
	assert.Contains(t, got, "1")
	assert.Contains(t, got, "3")

	t.Run("score none unless explain", func(t *testing.T) {
		var opts search.SearcherOptions
		inner := riot.NewBooleanQuery().SetBoost(0).AddMust(scoreSpy{riot.NewTermQuery("a").SetField("tag"), &opts})
		fc := &filterClause{BooleanQuery: inner}
		_, err := r.Search(context.Background(), riot.NewTopNSearch(10, riot.NewBooleanQuery().AddMust(fc)))
		require.NoError(t, err)
		assert.Equal(t, "none", opts.Score)

		_, err = r.Search(context.Background(), riot.NewTopNSearch(10, riot.NewBooleanQuery().AddMust(fc)).ExplainScores())
		require.NoError(t, err)
		assert.Empty(t, opts.Score)
	})
}
