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
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/uquery"
	"github.com/vcaesar/zedsearch/pkg/uquery/aggregation"
	"github.com/vcaesar/zedsearch/pkg/uquery/assoc"
	uquerydsl "github.com/vcaesar/zedsearch/pkg/uquery/query"
)

// searchExt holds the parts of a search resolved around the main query:
// rescoring of the top hits, suggesters and significant terms.
type searchExt struct {
	query     *meta.ZedQuery
	mappings  *meta.Mappings
	analyzers map[string]*analysis.Analyzer
	readers   []*riot.Reader
	rescorers []*uquery.Rescorer
	from      int
	size      int
}

// prepareSearch rewrites the rule and more_like_this queries, which need the
// stored rulesets and the readers, and widens the page to the rescore window.
func prepareSearch(ctx context.Context, query *meta.ZedQuery, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer, readers []*riot.Reader) (*searchExt, error) {
	ext := &searchExt{query: query, mappings: mappings, analyzers: analyzers, readers: readers}
	fns := map[string]uquery.TransformFunc{
		"rule": func(body map[string]interface{}) (map[string]interface{}, error) {
			return uquery.RuleQuery(body, metadata.QueryRules.Get)
		},
		"more_like_this": func(body map[string]interface{}) (map[string]interface{}, error) {
			return assoc.MoreLikeThis(ctx, readers, body, mappings, analyzers)
		},
	}
	rewritten, err := uquery.Transform(query.Query, fns)
	if err != nil {
		return nil, err
	}
	query.Query = rewritten

	if ext.rescorers, err = uquery.ParseRescore(query.Rescore); err != nil {
		return nil, err
	}
	for _, r := range ext.rescorers {
		if r.LTR != nil {
			if err := r.ResolveModel(metadata.LTRModels.Get); err != nil {
				return nil, err
			}
			for i, fq := range r.LTR.Queries {
				q, err := uquery.Transform(fq, fns)
				if err != nil {
					return nil, err
				}
				r.LTR.Queries[i], _ = q.(map[string]interface{})
			}
			continue
		}
		q, err := uquery.Transform(r.Query, fns)
		if err != nil {
			return nil, err
		}
		r.Query, _ = q.(map[string]interface{})
	}
	if len(ext.rescorers) > 0 {
		if !scoreSorted(query.Sort) {
			return nil, errors.New(errors.ErrorTypeIllegalArgumentException, "[rescore] cannot be used with a [sort] other than [_score] descending")
		}
		// bounding both before adding them keeps from + size from overflowing
		if query.From < 0 || query.Size < 0 || query.From > config.Global.MaxResults || query.Size > config.Global.MaxResults {
			return nil, errors.New(errors.ErrorTypeIllegalArgumentException,
				fmt.Sprintf("[rescore] from and size must be within [0, %d] but were [%d] and [%d]", config.Global.MaxResults, query.From, query.Size))
		}
		window := uquery.RescoreWindow(ext.rescorers, query.From, query.Size)
		if window > config.Global.MaxResults {
			return nil, errors.New(errors.ErrorTypeIllegalArgumentException,
				fmt.Sprintf("[rescore] from + size and window_size must be less than or equal to [%d] but was [%d]", config.Global.MaxResults, window))
		}
		ext.from, ext.size = query.From, query.Size
		query.Size, query.From = window, 0
	}
	return ext, nil
}

// restore gives the query back the page prepareSearch widened.
func (ext *searchExt) restore() {
	if len(ext.rescorers) > 0 {
		ext.query.From, ext.query.Size = ext.from, ext.size
	}
}

// scoreSorted reports whether sort orders hits by descending score only, the
// order rescoring keeps.
func scoreSorted(v interface{}) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimLeft(v, "+-") == "_score"
	case []interface{}:
		for _, item := range v {
			if !scoreSorted(item) {
				return false
			}
		}
		return true
	case map[string]interface{}:
		if len(v) != 1 {
			return false
		}
		order, ok := v["_score"]
		if !ok {
			return false
		}
		if m, isMap := order.(map[string]interface{}); isMap {
			order = m["order"]
		}
		s, _ := order.(string)
		return order == nil || strings.EqualFold(s, "desc")
	}
	return false
}

// finish rescores the hits of resp, adds suggestions and significant terms
// and evaluates the pipeline aggregations. It runs before the readers are
// closed.
func (ext *searchExt) finish(ctx context.Context, resp *meta.SearchResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ext.rescorers) > 0 {
		if err := ext.rescore(ctx, resp); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ext.query.Suggest) > 0 {
		suggest, err := assoc.Suggest(ctx, ext.readers, ext.query.Suggest, ext.mappings, ext.analyzers)
		if err != nil {
			return err
		}
		resp.Suggest = suggest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ext.query.Aggregations) > 0 && resp.Aggregations != nil {
		// #nosec G115 -- hit counts are non-negative
		fg := uint64(resp.Hits.Total.Value)
		if err := assoc.SignificantTerms(ctx, ext.readers, resp.Aggregations, ext.query.Aggregations, fg, ext.mappings, ext.analyzers); err != nil {
			return err
		}
		// pipeline aggregations read the final buckets, so they run last
		if err := aggregation.ApplyPipelines(resp.Aggregations, ext.query.Aggregations); err != nil {
			log.Printf("core.SearchV2: error applying pipeline aggregations: %s", err.Error())
		}
	}
	return ctx.Err()
}

// rescore applies each rescorer to its window of top hits, reorders all the
// hits, as Elasticsearch weights the hits past the window by query_weight,
// then cuts the requested page. The window is cut from the merged hits of
// all shards, not from the hits of each shard as Elasticsearch does.
// A learning_to_rank rescorer only reorders its window, which stays on top:
// model scores are not comparable with the scores past it.
func (ext *searchExt) rescore(ctx context.Context, resp *meta.SearchResponse) error {
	hits := resp.Hits.Hits
	for _, r := range ext.rescorers {
		window := hits[:min(r.WindowSize, len(hits))]
		if len(window) == 0 {
			continue
		}
		if r.LTR != nil && !r.LTR.Combine {
			scores, err := ext.ltrScores(ctx, r.LTR, window)
			if err != nil {
				return err
			}
			for i := range window {
				window[i].Score = scores[i]
			}
			sort.SliceStable(window, func(i, j int) bool { return window[i].Score > window[j].Score })
			continue
		}
		scores, err := ext.windowScores(ctx, r, window)
		if err != nil {
			return err
		}
		for i := range hits {
			if i >= len(window) {
				hits[i].Score *= r.QueryWeight
				continue
			}
			s, ok := scores[hitKey(hits[i].Index, hits[i].ID)]
			hits[i].Score = r.Combine(hits[i].Score, s, ok)
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	}
	resp.Hits.MaxScore = 0
	for _, h := range hits {
		resp.Hits.MaxScore = max(resp.Hits.MaxScore, h.Score)
	}
	start := min(ext.from, len(hits))
	end := min(start+ext.size, len(hits))
	resp.Hits.Hits = hits[start:end]
	return nil
}

// windowScores returns the rescore score of each hit of window matching the
// rescore query, of every hit for an sltr model.
func (ext *searchExt) windowScores(ctx context.Context, r *uquery.Rescorer, window []meta.Hit) (map[string]float64, error) {
	if r.LTR == nil {
		return ext.scoreHits(ctx, r.Query, window)
	}
	scores, err := ext.ltrScores(ctx, r.LTR, window)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]float64, len(window))
	for i, h := range window {
		byKey[hitKey(h.Index, h.ID)] = scores[i]
	}
	return byKey, nil
}

// ltrScores returns the model score of each hit of window: every feature
// query scores the window, a hit it does not match gets the feature default.
func (ext *searchExt) ltrScores(ctx context.Context, r *uquery.LTRRescore, window []meta.Hit) ([]float64, error) {
	features := make([][]float64, len(window))
	for i := range features {
		features[i] = make([]float64, len(r.Queries))
	}
	for j, q := range r.Queries {
		scores, err := ext.scoreHits(ctx, q, window)
		if err != nil {
			return nil, err
		}
		for i, h := range window {
			v, ok := scores[hitKey(h.Index, h.ID)]
			if !ok {
				v = r.Model.Features[j].Default
			}
			features[i][j] = v
		}
	}
	out := make([]float64, len(window))
	for i, x := range features {
		out[i] = r.Model.Score(x)
	}
	return out, nil
}

// scoreHits returns the score of rescore query q for each of hits matching it.
func (ext *searchExt) scoreHits(ctx context.Context, q map[string]interface{}, hits []meta.Hit) (map[string]float64, error) {
	ids := make([]interface{}, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	sub := map[string]interface{}{"bool": map[string]interface{}{
		"must":   q,
		"filter": map[string]interface{}{"ids": map[string]interface{}{"values": ids}},
	}}
	parsed, err := uquerydsl.Query(sub, ext.mappings, ext.analyzers)
	if err != nil {
		return nil, err
	}
	scores := make(map[string]float64, len(hits))
	for _, reader := range ext.readers {
		dmi, err := reader.Search(ctx, riot.NewTopNSearch(len(hits), parsed))
		if err != nil {
			return nil, err
		}
		next, err := dmi.Next()
		for err == nil && next != nil {
			var id, index string
			if verr := next.VisitStoredFields(func(field string, value []byte) bool {
				switch field {
				case "_id":
					id = string(value)
				case "_index":
					index = string(value)
				}
				return id == "" || index == ""
			}); verr != nil {
				return nil, verr
			}
			scores[hitKey(index, id)] = next.Score
			next, err = dmi.Next()
		}
		if err != nil {
			return nil, err
		}
	}
	return scores, nil
}

func hitKey(index, id string) string {
	return index + "\x00" + id
}
