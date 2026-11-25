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

package uquery

import (
	"fmt"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

const (
	normalizationProcessor = "normalization-processor"
	scoreRankerProcessor   = "score-ranker-processor"
)

// parseRank parses the legacy Elasticsearch rank.rrf, which fuses the query
// and each top-level knn search.
//
//	{"query": {...}, "knn": {...}, "rank": {"rrf": {"rank_window_size": 50, "rank_constant": 20}}}
func parseRank(q *meta.ZedQuery) (*Retriever, error) {
	m, err := object(q.Rank, "rank")
	if err != nil {
		return nil, err
	}
	body, ok := m["rrf"]
	if !ok || len(m) != 1 {
		return nil, parseError("[rank] supports only [rrf]")
	}
	b, err := object(body, "rank.rrf")
	if err != nil {
		return nil, err
	}
	r := &Retriever{Combination: CombineRRF, RankConstant: DefaultRankConstant}
	windowKey := ""
	for k, v := range b {
		switch k {
		case "rank_window_size", "window_size":
			var window int
			if window, err = positiveInt(v, "rrf."+k); err == nil && windowKey != "" && window != r.WindowSize {
				err = parseError(fmt.Sprintf("[rrf] [%s] conflicts with [%s]", k, windowKey))
			}
			r.WindowSize, windowKey = window, k
		case "rank_constant":
			r.RankConstant, err = positiveInt(v, "rrf.rank_constant")
		default:
			err = parseError(fmt.Sprintf("[rrf] unknown field [%s]", k))
		}
		if err != nil {
			return nil, err
		}
	}
	if q.Query != nil {
		query, err := object(q.Query, "query")
		if err != nil {
			return nil, err
		}
		r.Retrievers = append(r.Retrievers, &Retriever{Query: query})
	}
	knns, ok := q.Knn.([]interface{})
	if !ok && q.Knn != nil {
		knns = []interface{}{q.Knn}
	}
	for _, v := range knns {
		knn, err := object(v, "knn")
		if err != nil {
			return nil, err
		}
		r.Retrievers = append(r.Retrievers, &Retriever{Query: map[string]interface{}{"knn": knn}})
	}
	if len(r.Retrievers) < 2 {
		return nil, parseError("[rank] requires a [query] and a [knn], or two [knn] searches")
	}
	for range r.Retrievers {
		r.Weights = append(r.Weights, 1)
		r.Normalizers = append(r.Normalizers, NormalizeNone)
	}
	return r, nil
}

// hybridQuery returns the body of a top-level OpenSearch hybrid query.
func hybridQuery(v interface{}) (interface{}, bool) {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return nil, false
	}
	body, ok := m["hybrid"]
	return body, ok
}

// parseHybridQuery parses an OpenSearch hybrid query and the inline search
// pipeline fusing its sub-queries; min_max and arithmetic_mean without one.
//
//	{"hybrid": {"queries": [{"match": {...}}, {"knn": {...}}], "filter": {...}, "pagination_depth": 50}}
func parseHybridQuery(v, pipeline interface{}) (*Retriever, error) {
	b, err := object(v, "hybrid")
	if err != nil {
		return nil, err
	}
	r := &Retriever{Combination: CombineArithmetic}
	var queries, filters []interface{}
	for k, item := range b {
		switch k {
		case "queries":
			var ok bool
			if queries, ok = item.([]interface{}); !ok {
				return nil, parseError(fmt.Sprintf("[hybrid] queries must be an array, got %T", item))
			}
		case "filter":
			if filters, err = withFilters(nil, item); err != nil {
				return nil, err
			}
		case "pagination_depth":
			if r.WindowSize, err = positiveInt(item, "hybrid.pagination_depth"); err != nil {
				return nil, err
			}
		case "_name":
		default:
			return nil, parseError(fmt.Sprintf("[hybrid] unknown field [%s]", k))
		}
	}
	if len(queries) == 0 || len(queries) > maxHybridQueries {
		return nil, parseError(fmt.Sprintf("[hybrid] requires 1 to %d queries, got %d", maxHybridQueries, len(queries)))
	}
	normalizer := NormalizeMinMax
	if pipeline != nil {
		if normalizer, err = parsePipeline(pipeline, r); err != nil {
			return nil, err
		}
	}
	if r.Weights != nil && len(r.Weights) != len(queries) {
		return nil, parseError(fmt.Sprintf("[search_pipeline] number of weights [%d] must match the number of queries [%d]", len(r.Weights), len(queries)))
	}
	for i, item := range queries {
		query, err := object(item, "hybrid.queries")
		if err != nil {
			return nil, err
		}
		if query, err = hybridFiltered(query, filters); err != nil {
			return nil, err
		}
		r.Retrievers = append(r.Retrievers, &Retriever{Query: query})
		r.Normalizers = append(r.Normalizers, normalizer)
		if len(r.Weights) <= i {
			r.Weights = append(r.Weights, 1)
		}
	}
	return r, nil
}

// hybridFiltered returns a hybrid sub-query restricted to filters: a knn
// query filters before neighbor selection, as in OpenSearch.
func hybridFiltered(query map[string]interface{}, filters []interface{}) (map[string]interface{}, error) {
	knn, ok := query["knn"].(map[string]interface{})
	if !ok || len(query) != 1 {
		return filtered(query, filters), nil
	}
	knn, err := knnWithFilters(knn, filters)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"knn": knn}, nil
}

// parsePipeline parses an inline OpenSearch search pipeline with one phase
// results processor into r and returns its score normalizer.
//
//	{"phase_results_processors": [{"normalization-processor": {"normalization": {"technique": "min_max"},
//		"combination": {"technique": "arithmetic_mean", "parameters": {"weights": [0.3, 0.7]}}}}]}
//	{"phase_results_processors": [{"score-ranker-processor": {"combination": {"technique": "rrf", "rank_constant": 60}}}]}
func parsePipeline(v interface{}, r *Retriever) (string, error) {
	m, err := object(v, "search_pipeline")
	if err != nil {
		return "", err
	}
	normalizer := NormalizeMinMax
	for k, item := range m {
		switch k {
		case "description":
		case "phase_results_processors":
			list, ok := item.([]interface{})
			if !ok || len(list) != 1 {
				return "", parseError("[search_pipeline] phase_results_processors must hold one processor")
			}
			p, err := object(list[0], "phase_results_processors")
			if err != nil {
				return "", err
			}
			if len(p) != 1 {
				return "", parseError("[search_pipeline] a processor must have exactly one type")
			}
			for name, body := range p {
				if normalizer, err = parseProcessor(name, body, r); err != nil {
					return "", err
				}
			}
		default:
			return "", parseError(fmt.Sprintf("[search_pipeline] unsupported [%s]", k))
		}
	}
	return normalizer, nil
}

func parseProcessor(name string, v interface{}, r *Retriever) (string, error) {
	if name != normalizationProcessor && name != scoreRankerProcessor {
		return "", parseError(fmt.Sprintf("[search_pipeline] unsupported processor [%s]", name))
	}
	b, err := object(v, name)
	if err != nil {
		return "", err
	}
	normalizer := NormalizeMinMax
	if name == scoreRankerProcessor {
		r.Combination, r.RankConstant = CombineRRF, DefaultRankConstant
	}
	for k, item := range b {
		switch {
		case k == "description", k == "tag":
		case k == "normalization" && name == normalizationProcessor:
			if normalizer, err = parseTechnique(item); err != nil {
				return "", err
			}
		case k == "combination":
			if err = parseCombination(name, item, r); err != nil {
				return "", err
			}
		default:
			return "", parseError(fmt.Sprintf("[%s] unknown field [%s]", name, k))
		}
	}
	return normalizer, nil
}

// parseTechnique parses the normalization of a normalization-processor.
func parseTechnique(v interface{}) (string, error) {
	m, err := object(v, "normalization")
	if err != nil {
		return "", err
	}
	normalizer := NormalizeMinMax
	for k, item := range m {
		if k != "technique" {
			return "", parseError(fmt.Sprintf("[normalization] unknown field [%s]", k))
		}
		switch item {
		case "min_max":
		case "l2":
			normalizer = NormalizeL2
		default:
			return "", parseError(fmt.Sprintf("[normalization] unsupported technique [%v]", item))
		}
	}
	return normalizer, nil
}

func parseCombination(name string, v interface{}, r *Retriever) error {
	m, err := object(v, name+".combination")
	if err != nil {
		return err
	}
	for k, item := range m {
		switch {
		case k == "technique":
			s, _ := item.(string)
			switch {
			case name == scoreRankerProcessor && s == CombineRRF:
			case name == normalizationProcessor && (s == CombineArithmetic || s == CombineGeometric || s == CombineHarmonic):
				r.Combination = s
			default:
				return parseError(fmt.Sprintf("[%s] unsupported combination technique [%v]", name, item))
			}
		case k == "rank_constant" && name == scoreRankerProcessor:
			if r.RankConstant, err = positiveInt(item, "rank_constant"); err != nil {
				return err
			}
		case k == "parameters":
			if r.Weights, err = parseWeights(item, name); err != nil {
				return err
			}
		default:
			return parseError(fmt.Sprintf("[%s] unknown combination field [%s]", name, k))
		}
	}
	return nil
}

func parseWeights(v interface{}, name string) ([]float64, error) {
	m, err := object(v, name+".parameters")
	if err != nil {
		return nil, err
	}
	var weights []float64
	for k, item := range m {
		list, ok := item.([]interface{})
		if k != "weights" || !ok {
			return nil, parseError(fmt.Sprintf("[%s] parameters supports only an array of [weights]", name))
		}
		weights = make([]float64, 0, len(list))
		for _, w := range list {
			f, err := weight(w, name)
			if err != nil {
				return nil, err
			}
			weights = append(weights, f)
		}
	}
	return weights, nil
}
