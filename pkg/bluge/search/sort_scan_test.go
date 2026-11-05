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

package search

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery"
)

// scanDoc describes one test document: its values of "n" and whether it is
// deleted after indexing.
type scanDoc struct {
	vals    []float64
	deleted bool
}

// scanReader writes docs in batches of batchSize (one segment each) and
// returns a reader over them.
func scanTestReader(t *testing.T, docs []scanDoc, batchSize int) *riot.Reader {
	t.Helper()
	w, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, w.Close()) })

	for start := 0; start < len(docs); start += batchSize {
		batch := riot.NewBatch()
		for i := start; i < min(start+batchSize, len(docs)); i++ {
			doc := riot.NewDocument(fmt.Sprintf("d%d", i)).AddField(riot.NewKeywordField("k", "v").Sortable())
			for _, v := range docs[i].vals {
				doc.AddField(riot.NewNumericField("n", v).Sortable())
			}
			batch.Update(doc.ID(), doc)
		}
		require.NoError(t, w.Batch(batch))
	}
	batch := riot.NewBatch()
	for i, d := range docs {
		if d.deleted {
			batch.Delete(riot.Identifier(fmt.Sprintf("d%d", i)))
		}
	}
	require.NoError(t, w.Batch(batch))

	r, err := w.Reader()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, r.Close()) })
	return r
}

type scanResult struct {
	ids      []string
	scores   []float64
	hits     []int
	sorts    [][]byte
	count    uint64
	maxScore float64
}

func collectScan(t *testing.T, docs []*search.DocumentMatch, bucket *search.Bucket) scanResult {
	t.Helper()
	res := scanResult{count: bucket.Count(), maxScore: bucket.Metric("max_score")}
	for _, d := range docs {
		require.NoError(t, d.VisitStoredFields(func(field string, value []byte) bool {
			if field == "_id" {
				res.ids = append(res.ids, string(value))
			}
			return true
		}))
		res.scores = append(res.scores, d.Score)
		res.hits = append(res.hits, d.HitNumber)
		res.sorts = append(res.sorts, d.SortValue[0])
	}
	return res
}

func scanRequest(t *testing.T, query interface{}, sort string, from, size int) riot.SearchRequest {
	t.Helper()
	mappings := meta.NewMappings()
	mappings.SetProperty("n", meta.NewProperty("numeric"))
	mappings.SetProperty("k", meta.NewProperty("keyword"))
	req, err := uquery.ParseQueryDSL(&meta.ZedQuery{Query: query, Sort: sort, From: from, Size: size}, mappings, nil)
	require.NoError(t, err)
	return req
}

func TestSortScan(t *testing.T) {
	matchAll := map[string]interface{}{"match_all": map[string]interface{}{}}
	var ties, deletes []scanDoc
	for i := range 40 {
		ties = append(ties, scanDoc{vals: []float64{float64(i % 4)}})
		deletes = append(deletes, scanDoc{vals: []float64{float64(i*7%40) - 10.5}, deleted: i%3 == 0})
	}

	tests := []struct {
		name   string
		docs   []scanDoc
		sort   string
		from   int
		size   int
		scanOK bool
	}{
		{"desc ties", ties, "-n", 0, 10, true},
		{"asc ties", ties, "n", 0, 10, true},
		{"from", ties, "-n", 7, 5, true},
		{"from past end", ties, "-n", 50, 5, true},
		{"size larger than hits", ties, "n", 0, 100, true},
		{"size zero", ties, "n", 0, 0, true},
		{"deletes across segments", deletes, "-n", 3, 10, true},
		{"missing value", append([]scanDoc{{}}, ties...), "-n", 0, 10, false},
		{"multi valued", append([]scanDoc{{vals: []float64{1, 99}}}, ties...), "-n", 0, 10, false},
		{"keyword field", ties, "-k", 0, 10, false},
		{"score sort", ties, "_score", 0, 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := scanTestReader(t, tt.docs, 16)

			docs, bucket, ok, err := sortScan(context.Background(), r, scanRequest(t, matchAll, tt.sort, tt.from, tt.size))
			require.NoError(t, err)
			require.Equal(t, tt.scanOK, ok)
			if !ok {
				return
			}
			got := collectScan(t, docs, bucket)

			dmi, err := r.Search(context.Background(), scanRequest(t, matchAll, tt.sort, tt.from, tt.size))
			require.NoError(t, err)
			var want []*search.DocumentMatch
			next, err := dmi.Next()
			for err == nil && next != nil {
				want = append(want, next)
				next, err = dmi.Next()
			}
			require.NoError(t, err)
			assert.Equal(t, collectScan(t, want, dmi.Aggregations()), got)
		})
	}

	t.Run("not match_all", func(t *testing.T) {
		r := scanTestReader(t, ties, 16)
		term := map[string]interface{}{"term": map[string]interface{}{"k": "v"}}
		_, _, ok, err := sortScan(context.Background(), r, scanRequest(t, term, "-n", 0, 10))
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("canceled", func(t *testing.T) {
		r := scanTestReader(t, ties, 16)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, _, err := sortScan(ctx, r, scanRequest(t, matchAll, "-n", 0, 10))
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// TestMultiSearchSortScan checks the merged result of scanned shards is the
// one the collectors give, including the single reader path.
func TestMultiSearchSortScan(t *testing.T) {
	readers := shardReaders(t, 3, 5) // n = 0..14
	mappings := meta.NewMappings()
	mappings.SetProperty("n", meta.NewProperty("numeric"))
	for _, n := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d readers", n), func(t *testing.T) {
			query := &meta.ZedQuery{Query: map[string]interface{}{"match_all": map[string]interface{}{}}, Sort: "n", From: 2, Size: 3}
			dmi, err := MultiSearch(context.Background(), query, mappings, nil, readers[:n]...)
			require.NoError(t, err)
			list, ok := dmi.(*DocumentList)
			require.True(t, ok, "scan results are merged by DocumentList")
			var ids []string
			for next, err := list.Next(); next != nil; next, err = list.Next() {
				require.NoError(t, err)
				require.NoError(t, next.VisitStoredFields(func(field string, value []byte) bool {
					if field == "_id" {
						ids = append(ids, string(value))
					}
					return true
				}))
			}
			assert.Equal(t, []string{"0-2", "0-3", "0-4"}, ids)
			assert.Equal(t, uint64(5*n), list.Aggregations().Count())
			assert.Equal(t, 1.0, list.Aggregations().Metric("max_score"))
		})
	}
}

func TestScanReaderNoDocValues(t *testing.T) {
	_, err := scanReader{}.DocumentValueReader([]string{"n"})
	assert.ErrorIs(t, err, errScanNoDocValues)
}
