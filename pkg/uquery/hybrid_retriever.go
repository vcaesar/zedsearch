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
	"strings"

	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// parseRetriever parses an Elasticsearch retriever: standard, knn, rrf or
// linear. filters are the filters of the parent retrievers, applied to each
// leaf.
//
//	{"rrf": {"retrievers": [{"standard": {"query": {...}}}, {"knn": {...}}], "rank_window_size": 50, "rank_constant": 60}}
//	{"linear": {"retrievers": [{"retriever": {...}, "weight": 2, "normalizer": "minmax"}], "normalizer": "l2_norm"}}
func parseRetriever(v interface{}, filters []interface{}) (*Retriever, error) {
	m, err := object(v, "retriever")
	if err != nil {
		return nil, err
	}
	if len(m) == 1 {
		for name, body := range m {
			return parseNamedRetriever(name, body, filters)
		}
	}
	return nil, parseError(fmt.Sprintf("[retriever] must have exactly one retriever, got %d", len(m)))
}

func parseNamedRetriever(name string, body interface{}, filters []interface{}) (*Retriever, error) {
	b, err := object(body, name)
	if err != nil {
		return nil, err
	}
	switch name {
	case "standard":
		return parseStandard(b, filters)
	case "knn":
		return parseKnnRetriever(b, filters)
	case "rrf", "linear":
		return parseFusion(name, b, filters)
	}
	return nil, parseError(fmt.Sprintf("[retriever] unknown retriever [%s]", name))
}

func parseStandard(b map[string]interface{}, filters []interface{}) (*Retriever, error) {
	query := map[string]interface{}{"match_all": map[string]interface{}{}}
	var err error
	for k, v := range b {
		switch k {
		case "query":
			if query, err = object(v, "standard.query"); err != nil {
				return nil, err
			}
		case "filter":
			if filters, err = withFilters(filters, v); err != nil {
				return nil, err
			}
		case "_name":
		default:
			return nil, parseError(fmt.Sprintf("[standard] unknown field [%s]", k))
		}
	}
	return &Retriever{Query: filtered(query, filters)}, nil
}

// parseKnnRetriever returns the knn query of an Elasticsearch knn retriever,
// whose options are the ones of the top-level knn. The parent filters join
// the knn filter, applied before ranking.
func parseKnnRetriever(b map[string]interface{}, filters []interface{}) (*Retriever, error) {
	knn, err := knnWithFilters(b, filters)
	if err != nil {
		return nil, err
	}
	return &Retriever{Query: map[string]interface{}{"knn": knn}}, nil
}

func parseFusion(name string, b map[string]interface{}, filters []interface{}) (*Retriever, error) {
	r := &Retriever{Combination: CombineRRF, RankConstant: DefaultRankConstant}
	if name == "linear" {
		r.Combination = CombineSum
	}
	normalizer := NormalizeNone
	var children []interface{}
	var err error
	for k, v := range b {
		switch {
		case k == "retrievers":
			var ok bool
			if children, ok = v.([]interface{}); !ok {
				return nil, parseError(fmt.Sprintf("[%s] retrievers must be an array, got %T", name, v))
			}
		case k == "rank_window_size":
			if r.WindowSize, err = positiveInt(v, name+".rank_window_size"); err != nil {
				return nil, err
			}
		case k == "rank_constant" && name == "rrf":
			if r.RankConstant, err = positiveInt(v, "rrf.rank_constant"); err != nil {
				return nil, err
			}
		case k == "normalizer" && name == "linear":
			if normalizer, err = parseNormalizer(v); err != nil {
				return nil, err
			}
		case k == "min_score":
			min, err := zutils.ToFloat64(v)
			if err != nil {
				return nil, parseError(fmt.Sprintf("[%s] min_score must be a number, got %v", name, v))
			}
			r.MinScore = &min
		case k == "filter":
			if filters, err = withFilters(filters, v); err != nil {
				return nil, err
			}
		case k == "_name":
		default:
			return nil, parseError(fmt.Sprintf("[%s] unknown field [%s]", name, k))
		}
	}
	if len(children) == 0 {
		return nil, parseError(fmt.Sprintf("[%s] requires at least one retriever in [retrievers]", name))
	}
	for _, item := range children {
		child, w, norm, err := parseComponent(name, item, normalizer, filters)
		if err != nil {
			return nil, err
		}
		r.Retrievers = append(r.Retrievers, child)
		r.Weights = append(r.Weights, w)
		r.Normalizers = append(r.Normalizers, norm)
	}
	return r, nil
}

// parseComponent parses a child of a fusion retriever: a retriever, or one
// wrapped with its weight and, for linear, its normalizer.
func parseComponent(name string, item interface{}, normalizer string, filters []interface{}) (*Retriever, float64, string, error) {
	m, err := object(item, name+".retrievers")
	if err != nil {
		return nil, 0, "", err
	}
	inner, wrapped := m["retriever"]
	if !wrapped {
		r, err := parseRetriever(m, filters)
		return r, 1, normalizer, err
	}
	w := 1.0
	for k, v := range m {
		switch {
		case k == "retriever":
		case k == "weight":
			if w, err = weight(v, name); err != nil {
				return nil, 0, "", err
			}
		case k == "normalizer" && name == "linear":
			if normalizer, err = parseNormalizer(v); err != nil {
				return nil, 0, "", err
			}
		default:
			return nil, 0, "", parseError(fmt.Sprintf("[%s] unknown field [%s] of a retriever", name, k))
		}
	}
	r, err := parseRetriever(inner, filters)
	return r, w, normalizer, err
}

func parseNormalizer(v interface{}) (string, error) {
	s, _ := v.(string)
	switch s = strings.ToLower(s); s {
	case NormalizeNone, NormalizeMinMax, NormalizeL2:
		return s, nil
	}
	return "", parseError(fmt.Sprintf("[linear] unknown normalizer [%v]", v))
}
