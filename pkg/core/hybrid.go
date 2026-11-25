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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

package core

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery"
)

// hybridSearch runs the phases of a search fusing several retrievers.
type hybridSearch struct {
	query    *meta.ZedQuery
	run      func(*meta.ZedQuery) (*meta.SearchResponse, error)
	took     int
	deadline time.Time // of all the phases, zero without timeout
}

// searchHybrid answers a search uquery.Hybrid fuses: run searches each leaf
// retriever on its own, r fuses their ranked hits, then run fetches the page
// of fused hits, and counts hits.total and the aggregations over the union
// of the leaves.
func searchHybrid(query *meta.ZedQuery, r *uquery.Retriever, run func(*meta.ZedQuery) (*meta.SearchResponse, error)) (*meta.SearchResponse, error) {
	if query.Rescore != nil {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException, "[rescore] cannot be used with a hybrid search")
	}
	if !scoreSorted(query.Sort) {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException, "[sort] other than [_score] cannot be used with a hybrid search")
	}
	maxResults := config.Global.MaxResults
	if query.From < 0 || query.Size < 0 || query.From > maxResults || query.Size > maxResults {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException,
			fmt.Sprintf("[hybrid] from and size must be within [0, %d] but were [%d] and [%d]", maxResults, query.From, query.Size))
	}
	// the default window does not grow with from: pages of one search must
	// rank the same hits, deeper pages need an explicit window
	window := r.WindowSize
	if window == 0 {
		window = max(uquery.DefaultRankWindow, query.Size)
	}
	if window > maxResults {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException,
			fmt.Sprintf("[hybrid] rank window must be less than or equal to [%d] but was [%d]", maxResults, window))
	}

	h := &hybridSearch{query: query, run: run}
	if query.Timeout > 0 {
		h.deadline = time.Now().Add(time.Duration(query.Timeout) * time.Second)
	}
	ranked, err := h.retrieve(r, window)
	if err != nil {
		return searchFailed(err)
	}
	leaves := r.Leaves()
	union := leaves[0]
	if len(leaves) > 1 {
		union = map[string]interface{}{"bool": map[string]interface{}{"should": leaves}}
	}
	resp, err := h.search(&meta.ZedQuery{
		Query:          union,
		Aggregations:   query.Aggregations,
		Suggest:        query.Suggest,
		TrackTotalHits: query.TrackTotalHits,
		Source:         false,
	})
	if err != nil {
		return searchFailed(err)
	}
	if r.MinScore != nil {
		// min_score leaves out hits of the union
		resp.Hits.Total = meta.Total{Value: len(ranked)}
	}
	start := min(query.From, len(ranked))
	page := ranked[start:min(start+query.Size, len(ranked))]
	if resp.Hits.Hits, err = h.fetch(page, leaves); err != nil {
		return searchFailed(err)
	}
	resp.Hits.MaxScore = 0
	if len(ranked) > 0 {
		resp.Hits.MaxScore = ranked[0].Score
	}
	resp.Took = h.took
	return resp, nil
}

// search runs q within the deadline of the hybrid search, its timeout the
// whole seconds left. A timed out phase fails it with
// context.DeadlineExceeded.
func (h *hybridSearch) search(q *meta.ZedQuery) (*meta.SearchResponse, error) {
	if !h.deadline.IsZero() {
		left := time.Until(h.deadline)
		if left <= 0 {
			return nil, context.DeadlineExceeded
		}
		q.Timeout = int(math.Ceil(left.Seconds()))
	}
	resp, err := h.run(q)
	if err != nil {
		return nil, err
	}
	if resp.TimedOut {
		return nil, context.DeadlineExceeded
	}
	h.took += resp.Took
	return resp, nil
}

// retrieve returns the top window hits of r: the hits of a leaf query, the
// fused hits of the children of a fusion.
func (h *hybridSearch) retrieve(r *uquery.Retriever, window int) ([]uquery.RankedHit, error) {
	if r.Query != nil {
		resp, err := h.search(&meta.ZedQuery{Query: r.Query, Size: window, Source: false})
		if err != nil {
			return nil, err
		}
		hits := make([]uquery.RankedHit, len(resp.Hits.Hits))
		for i, hit := range resp.Hits.Hits {
			hits[i] = uquery.RankedHit{Index: hit.Index, ID: hit.ID, Score: hit.Score}
		}
		return hits, nil
	}
	if r.WindowSize > 0 {
		window = r.WindowSize
	}
	lists := make([][]uquery.RankedHit, len(r.Retrievers))
	for i, child := range r.Retrievers {
		var err error
		if lists[i], err = h.retrieve(child, window); err != nil {
			return nil, err
		}
	}
	return r.Fuse(lists, window), nil
}

// fetch renders the hits of page, in its order and with its fused scores.
// With highlight the leaf queries match the hits again for their terms. A
// hit deleted since it was ranked is left out.
func (h *hybridSearch) fetch(page []uquery.RankedHit, leaves []interface{}) ([]meta.Hit, error) {
	if len(page) == 0 {
		return []meta.Hit{}, nil
	}
	// the ids of each index: an id may be in other indexes too
	idsByIndex := make(map[string][]interface{})
	var indexes []string
	for _, hit := range page {
		if _, ok := idsByIndex[hit.Index]; !ok {
			indexes = append(indexes, hit.Index)
		}
		idsByIndex[hit.Index] = append(idsByIndex[hit.Index], hit.ID)
	}
	pairs := make([]interface{}, len(indexes))
	for i, index := range indexes {
		pairs[i] = map[string]interface{}{"bool": map[string]interface{}{"filter": []interface{}{
			map[string]interface{}{"term": map[string]interface{}{"_index": index}},
			map[string]interface{}{"ids": map[string]interface{}{"values": idsByIndex[index]}},
		}}}
	}
	body := map[string]interface{}{"filter": map[string]interface{}{"bool": map[string]interface{}{"should": pairs}}}
	if h.query.Highlight != nil {
		body["should"] = leaves
	}
	resp, err := h.search(&meta.ZedQuery{
		Query:     map[string]interface{}{"bool": body},
		Size:      len(page),
		Source:    h.query.Source,
		Fields:    h.query.Fields,
		Highlight: h.query.Highlight,
	})
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]meta.Hit, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		byKey[hitKey(hit.Index, hit.ID)] = hit
	}
	hits := make([]meta.Hit, 0, len(page))
	for _, ranked := range page {
		if hit, ok := byKey[hitKey(ranked.Index, ranked.ID)]; ok {
			hit.Score = ranked.Score
			hits = append(hits, hit)
		}
	}
	return hits, nil
}
