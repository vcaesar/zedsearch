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
please refer to [AGPL-3.0](./LICENSE-AGPL)) and LICENSE-EE(./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

package uquery

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

const (
	// DefaultRankWindow is the Elasticsearch default rank_window_size.
	DefaultRankWindow = 10
	// DefaultRankConstant is the RRF rank constant of Elasticsearch and OpenSearch.
	DefaultRankConstant = 60
	// maxHybridQueries is the OpenSearch limit of sub-queries of a hybrid query.
	maxHybridQueries = 5
)

// Fusion combinations: rrf sums the weighted reciprocal ranks, sum the
// weighted normalized scores (Elasticsearch linear), the means are the
// OpenSearch normalization-processor ones.
const (
	CombineRRF        = "rrf"
	CombineSum        = "sum"
	CombineArithmetic = "arithmetic_mean"
	CombineGeometric  = "geometric_mean"
	CombineHarmonic   = "harmonic_mean"
)

// Score normalizers of a score fusion.
const (
	NormalizeNone   = "none"
	NormalizeMinMax = "minmax"
	NormalizeL2     = "l2_norm"
)

// Retriever is a node of a hybrid search: a leaf ranks the hits of Query, a
// fusion node fuses the ranked hits of its Retrievers.
type Retriever struct {
	Query        map[string]interface{}
	Retrievers   []*Retriever
	Weights      []float64 // by retriever
	Normalizers  []string  // by retriever, unused by rrf
	Combination  string
	RankConstant int
	WindowSize   int // 0 takes the window of the parent
	MinScore     *float64
}

// RankedHit is a hit of a retriever, in rank order.
type RankedHit struct {
	Index string
	ID    string
	Score float64
}

// Hybrid returns the fusion of a search with an Elasticsearch retriever, a
// legacy rank.rrf or an OpenSearch hybrid query, nil for any other search.
// A retriever without fusion, standard or knn alone, becomes the query of q.
func Hybrid(q *meta.ZedQuery) (*Retriever, error) {
	if q.Retriever != nil {
		if q.Query != nil || q.Knn != nil || q.Rank != nil {
			return nil, parseError("[retriever] cannot be used with [query], [knn] or [rank]")
		}
		r, err := parseRetriever(q.Retriever, nil)
		if err != nil || r.Query == nil {
			return r, err
		}
		q.Query, q.Retriever = r.Query, nil
		return nil, nil
	}
	if q.Rank != nil {
		return parseRank(q)
	}
	if body, ok := hybridQuery(q.Query); ok {
		if q.Knn != nil {
			return nil, parseError("[knn] cannot be used with a [hybrid] query, add it to [hybrid.queries]")
		}
		return parseHybridQuery(body, q.SearchPipeline)
	}
	return nil, nil
}

// Leaves returns the queries of the leaf retrievers of r.
func (r *Retriever) Leaves() []interface{} {
	if r.Query != nil {
		return []interface{}{r.Query}
	}
	var leaves []interface{}
	for _, child := range r.Retrievers {
		leaves = append(leaves, child.Leaves()...)
	}
	return leaves
}

// HasMinScore reports whether r or a retriever below it has a min_score.
func (r *Retriever) HasMinScore() bool {
	if r.MinScore != nil {
		return true
	}
	for _, child := range r.Retrievers {
		if child.HasMinScore() {
			return true
		}
	}
	return false
}

func parseError(reason string) error {
	return errors.New(errors.ErrorTypeParsingException, reason)
}

func object(v interface{}, name string) (map[string]interface{}, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, parseError(fmt.Sprintf("[%s] must be an object, got %T", name, v))
	}
	return m, nil
}

func positiveInt(v interface{}, name string) (int, error) {
	n, err := zutils.ToInt(v)
	if f, ok := v.(float64); err != nil || n <= 0 || ok && f != math.Trunc(f) {
		return 0, parseError(fmt.Sprintf("[%s] must be a positive integer, got %v", name, v))
	}
	return n, nil
}

func weight(v interface{}, name string) (float64, error) {
	w, err := zutils.ToFloat64(v)
	if err != nil || w < 0 || math.IsNaN(w) || math.IsInf(w, 0) {
		return 0, parseError(fmt.Sprintf("[%s] weight must be a non-negative number, got %v", name, v))
	}
	return w, nil
}

// withFilters returns filters and the filters of v, one query or an array
// of queries, in a new slice: siblings must not share the backing array.
func withFilters(filters []interface{}, v interface{}) ([]interface{}, error) {
	list, ok := v.([]interface{})
	if !ok {
		list = []interface{}{v}
	}
	for _, f := range list {
		if _, err := object(f, "filter"); err != nil {
			return nil, err
		}
	}
	return append(slices.Clip(filters), list...), nil
}

// knnWithFilters returns the knn options with filters joining their filter,
// applied before neighbor selection. Field-keyed options keep their one key.
// Options are case-insensitive, as KnnQuery reads them: every spelling of
// filter joins the one canonical filter.
func knnWithFilters(knn map[string]interface{}, filters []interface{}) (map[string]interface{}, error) {
	if len(filters) == 0 {
		return knn, nil
	}
	if len(knn) == 1 {
		for field, v := range knn {
			if opts, ok := v.(map[string]interface{}); ok && !strings.EqualFold(field, "filter") && !strings.EqualFold(field, "method_parameters") {
				opts, err := knnWithFilters(opts, filters)
				return map[string]interface{}{field: opts}, err
			}
		}
	}
	out := make(map[string]interface{}, len(knn)+1)
	for _, k := range slices.Sorted(maps.Keys(knn)) {
		if !strings.EqualFold(k, "filter") {
			out[k] = knn[k]
			continue
		}
		var err error
		if filters, err = withFilters(filters, knn[k]); err != nil {
			return nil, err
		}
	}
	out["filter"] = filters
	return out, nil
}

// filtered returns query restricted to filters.
func filtered(query map[string]interface{}, filters []interface{}) map[string]interface{} {
	if len(filters) == 0 {
		return query
	}
	return map[string]interface{}{"bool": map[string]interface{}{"must": query, "filter": filters}}
}

// Fuse fuses lists, the ranked hits of each retriever of r, into the hits
// ranked by fused score, cut to window. Ties keep the order the hits are
// first seen in.
func (r *Retriever) Fuse(lists [][]RankedHit, window int) []RankedHit {
	type fused struct {
		hit    RankedHit
		scores []float64 // by retriever: the rank for rrf, NaN when absent
	}
	byKey := make(map[string]int)
	var docs []fused
	for i, list := range lists {
		scores := r.normalize(i, list)
		for rank, h := range list {
			key := h.Index + "\x00" + h.ID
			j, ok := byKey[key]
			if !ok {
				j = len(docs)
				byKey[key] = j
				s := make([]float64, len(lists))
				for k := range s {
					s[k] = math.NaN()
				}
				docs = append(docs, fused{hit: h, scores: s})
			}
			docs[j].scores[i] = scores[rank]
			if r.Combination == CombineRRF {
				docs[j].scores[i] = float64(rank + 1)
			}
		}
	}
	out := make([]RankedHit, 0, len(docs))
	for _, d := range docs {
		d.hit.Score = r.combine(d.scores)
		if r.MinScore == nil || d.hit.Score >= *r.MinScore {
			out = append(out, d.hit)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out[:min(window, len(out))]
}

// combine returns the fused score of a hit from its scores by retriever.
// As in OpenSearch, arithmetic_mean counts a missing score as 0, the other
// means skip the scores that are not positive.
func (r *Retriever) combine(scores []float64) float64 {
	var sum, weights float64
	for i, s := range scores {
		w := r.Weights[i]
		switch {
		case math.IsNaN(s):
			if r.Combination == CombineArithmetic {
				weights += w
			}
		case r.Combination == CombineRRF:
			sum += w / (float64(r.RankConstant) + s)
		case r.Combination == CombineSum:
			sum += w * s
		case r.Combination == CombineArithmetic:
			sum, weights = sum+w*s, weights+w
		case s <= 0:
		case r.Combination == CombineGeometric:
			sum, weights = sum+w*math.Log(s), weights+w
		case r.Combination == CombineHarmonic:
			sum, weights = sum+w/s, weights+w
		}
	}
	switch r.Combination {
	case CombineArithmetic:
		if weights > 0 {
			return sum / weights
		}
		return 0
	case CombineGeometric:
		if weights > 0 {
			return math.Exp(sum / weights)
		}
		return 0
	case CombineHarmonic:
		if sum > 0 {
			return weights / sum
		}
		return 0
	}
	return sum
}

// normalize returns the scores of list normalized as retriever i asks, as
// the Elasticsearch minmax and l2_norm normalizers do.
func (r *Retriever) normalize(i int, list []RankedHit) []float64 {
	const epsilon = 1e-6
	scores := make([]float64, len(list))
	lo, hi, squares := math.Inf(1), math.Inf(-1), 0.0
	for j, h := range list {
		scores[j] = h.Score
		lo, hi, squares = math.Min(lo, h.Score), math.Max(hi, h.Score), squares+h.Score*h.Score
	}
	switch r.Normalizers[i] {
	case NormalizeMinMax:
		for j, s := range scores {
			scores[j] = 1
			if hi-lo >= epsilon {
				scores[j] = (s - lo) / (hi - lo)
			}
		}
	case NormalizeL2:
		if norm := math.Sqrt(squares); norm >= epsilon {
			for j, s := range scores {
				scores[j] = s / norm
			}
		}
	}
	return scores
}
