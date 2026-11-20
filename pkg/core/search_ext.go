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
	"sort"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/uquery"
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
func prepareSearch(query *meta.ZedQuery, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer, readers []*riot.Reader) (*searchExt, error) {
	ext := &searchExt{query: query, mappings: mappings, analyzers: analyzers, readers: readers}
	rewritten, err := uquery.Transform(query.Query, map[string]uquery.TransformFunc{
		"rule": func(body map[string]interface{}) (map[string]interface{}, error) {
			return uquery.RuleQuery(body, metadata.QueryRules.Get)
		},
		"more_like_this": func(body map[string]interface{}) (map[string]interface{}, error) {
			return assoc.MoreLikeThis(readers, body, mappings, analyzers)
		},
	})
	if err != nil {
		return nil, err
	}
	query.Query = rewritten

	if ext.rescorers, err = uquery.ParseRescore(query.Rescore); err != nil {
		return nil, err
	}
	if len(ext.rescorers) > 0 {
		ext.from, ext.size = query.From, query.Size
		query.Size, query.From = uquery.RescoreWindow(ext.rescorers, query.From, query.Size), 0
	}
	return ext, nil
}

// finish rescores the hits of resp and adds suggestions and significant
// terms. It runs before the readers are closed.
func (ext *searchExt) finish(resp *meta.SearchResponse) error {
	if len(ext.rescorers) > 0 {
		if err := ext.rescore(resp); err != nil {
			return err
		}
	}
	if len(ext.query.Suggest) > 0 {
		suggest, err := assoc.Suggest(ext.readers, ext.query.Suggest, ext.mappings, ext.analyzers)
		if err != nil {
			return err
		}
		resp.Suggest = suggest
	}
	if len(ext.query.Aggregations) > 0 && resp.Aggregations != nil {
		// #nosec G115 -- hit counts are non-negative
		fg := uint64(resp.Hits.Total.Value)
		if err := assoc.SignificantTerms(ext.readers, resp.Aggregations, ext.query.Aggregations, fg, ext.mappings, ext.analyzers); err != nil {
			return err
		}
	}
	return nil
}

// rescore applies each rescorer to its window of top hits, then cuts the
// requested page.
func (ext *searchExt) rescore(resp *meta.SearchResponse) error {
	hits := resp.Hits.Hits
	for _, r := range ext.rescorers {
		window := hits[:min(r.WindowSize, len(hits))]
		if len(window) == 0 {
			continue
		}
		scores, err := ext.scoreHits(r.Query, window)
		if err != nil {
			return err
		}
		for i := range window {
			s, ok := scores[hitKey(window[i].Index, window[i].ID)]
			window[i].Score = r.Combine(window[i].Score, s, ok)
		}
		sort.SliceStable(window, func(i, j int) bool { return window[i].Score > window[j].Score })
	}
	resp.Hits.MaxScore = 0
	for _, h := range hits {
		resp.Hits.MaxScore = max(resp.Hits.MaxScore, h.Score)
	}
	start := min(ext.from, len(hits))
	end := min(start+ext.size, len(hits))
	resp.Hits.Hits = hits[start:end]
	ext.query.From, ext.query.Size = ext.from, ext.size
	return nil
}

// scoreHits returns the score of rescore query q for each of hits matching it.
func (ext *searchExt) scoreHits(q map[string]interface{}, hits []meta.Hit) (map[string]float64, error) {
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
		dmi, err := reader.Search(context.Background(), riot.NewTopNSearch(len(hits), parsed))
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
