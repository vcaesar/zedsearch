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
	"container/heap"
	"context"
	"errors"
	"slices"

	segment "github.com/vcaesar/bluge_segment_api"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/numeric"
	"github.com/vcaesar/riot/search"
)

// matchAllScore is the score riot's MatchAllSearcher gives every hit.
const matchAllScore = 1.0

var errScanNoDocValues = errors.New("sort scan hits have no doc values")

// scanSortField returns the field and direction when req is a match_all
// ordered by one numeric doc value with only score aggregations, the case
// sortScan answers from the numeric column.
func scanSortField(req riot.SearchRequest) (field string, desc, ok bool) {
	topN, ok := req.(*riot.TopNSearch)
	if !ok {
		return "", false, false
	}
	if _, ok := topN.Query().(*riot.MatchAllQuery); !ok {
		return "", false, false
	}
	if opts := topN.Options(); opts.ExplainScores || opts.IncludeLocations {
		return "", false, false
	}
	order := topN.SortOrder()
	if len(order) != 1 {
		return "", false, false
	}
	return order.NumericFirst()
}

// sortScan answers a match_all sorted by a numeric field by scanning the
// field's numeric column of r instead of building a match per document.
// The hits, scores, order (ties by doc order) and aggregations are those of
// r.Search. ok is false, with nothing done, when the request does not
// qualify or a live document does not hold exactly one value of field;
// the caller then runs r.Search.
func sortScan(ctx context.Context, r *riot.Reader, req riot.SearchRequest) (
	docs []*search.DocumentMatch, bucket *search.Bucket, ok bool, err error) {
	field, desc, ok := scanSortField(req)
	if !ok {
		return nil, nil, false, nil
	}
	topN := req.(*riot.TopNSearch)
	bucket = search.NewBucket("", topN.Aggregations())
	if !bucket.ScoreOnly() {
		return nil, nil, false, nil
	}
	live, err := r.Count()
	if err != nil {
		return nil, nil, false, err
	}

	top := &scanHeap{desc: desc, k: topN.Size() + topN.From()}
	var n uint64
	var last uint64
	single := true
	visit := func(doc uint64, v int64) {
		if n > 0 && doc <= last {
			single = false // multi-valued, it sorts by another value
		}
		last = doc
		n++
		top.offer(scanHit{value: v, doc: doc, hit: int(n)})
	}
	err = r.VisitInt64Blocks(field, func(base uint64, docNums []uint64, vals []int64) error {
		if docNums == nil {
			for i, v := range vals {
				visit(base+uint64(i), v)
			}
		} else {
			for i, v := range vals {
				visit(docNums[i], v)
			}
		}
		if !single {
			return errScanFallback
		}
		return nil
	})
	if err != nil || n != live {
		// a segment without a numeric column, a missing or extra value:
		// the general path handles these, and reports a real error itself
		return nil, nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}

	if n > 0 {
		bucket.ConsumeScore(matchAllScore, int(n))
	}
	bucket.Finish()

	hits := top.sorted()
	if from := topN.From(); from < len(hits) {
		hits = hits[from:]
	} else {
		hits = nil
	}
	reader := scanReader{r}
	docs = make([]*search.DocumentMatch, len(hits))
	for i, h := range hits {
		d := &search.DocumentMatch{
			Number:    h.doc,
			Score:     matchAllScore,
			SortValue: [][]byte{numeric.MustNewPrefixCodedInt64(h.value, 0)},
			HitNumber: h.hit,
		}
		d.SetReader(reader)
		docs[i] = d
	}
	return docs, bucket, true, nil
}

var errScanFallback = errors.New("sort scan fallback")

// scanReader lets sort scan hits load stored fields from their reader.
type scanReader struct{ r *riot.Reader }

func (s scanReader) VisitStoredFields(number uint64, visitor segment.StoredFieldVisitor) error {
	return s.r.VisitStoredFields(number, riot.StoredFieldVisitor(visitor))
}

func (s scanReader) DocumentValueReader([]string) (segment.DocumentValueReader, error) {
	return nil, errScanNoDocValues
}

type scanHit struct {
	value int64
	doc   uint64
	hit   int
}

// scanHeap keeps the best k hits with the worst at the root.
type scanHeap struct {
	hits []scanHit
	k    int
	desc bool
}

// before reports whether a sorts before b: by value, then by doc order.
func (h *scanHeap) before(a, b scanHit) bool {
	if a.value != b.value {
		return (a.value > b.value) == h.desc
	}
	return a.hit < b.hit
}

func (h *scanHeap) offer(x scanHit) {
	switch {
	case len(h.hits) < h.k:
		h.hits = append(h.hits, x)
		if len(h.hits) == h.k {
			heap.Init(h)
		}
	case h.k > 0 && h.before(x, h.hits[0]):
		h.hits[0] = x
		heap.Fix(h, 0)
	}
}

// sorted returns the kept hits in result order.
func (h *scanHeap) sorted() []scanHit {
	slices.SortFunc(h.hits, func(a, b scanHit) int {
		if h.before(a, b) {
			return -1
		}
		return 1
	})
	return h.hits
}

func (h *scanHeap) Len() int           { return len(h.hits) }
func (h *scanHeap) Less(i, j int) bool { return h.before(h.hits[j], h.hits[i]) }
func (h *scanHeap) Swap(i, j int)      { h.hits[i], h.hits[j] = h.hits[j], h.hits[i] }
func (h *scanHeap) Push(any)           { panic("scanHeap: Push is not used") }
func (h *scanHeap) Pop() any           { panic("scanHeap: Pop is not used") }
