/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI
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
	"fmt"
	"strings"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// BoostingQuery returns the documents matching positive, scoring those that
// also match negative with their score multiplied by negative_boost.
func BoostingQuery(query map[string]interface{}, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) (riot.Query, error) {
	q := &boostingQuery{negativeBoost: -1, boost: 1}
	for k, v := range query {
		key := strings.ToLower(k)
		switch key {
		case "positive", "negative":
			vv, ok := v.(map[string]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[boosting] %s doesn't support values of type: %T", key, v))
			}
			subq, err := Query(vv, mappings, analyzers)
			if err != nil {
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[boosting] failed to parse [%s]", key)).Cause(err)
			}
			if key == "positive" {
				q.positive = subq
			} else {
				q.negative = subq
			}
		case "negative_boost", "boost":
			f, err := zutils.ToFloat64(v)
			if err != nil {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[boosting] %s doesn't support values of type: %T", key, v))
			}
			if key == "boost" {
				q.boost = f
			} else {
				q.negativeBoost = f
			}
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[boosting] unknown field [%s]", k))
		}
	}
	if q.positive == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[boosting] requires [positive] query")
	}
	if q.negative == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[boosting] requires [negative] query")
	}
	if q.negativeBoost < 0 || q.negativeBoost > 1 {
		return nil, errors.New(errors.ErrorTypeParsingException, "[boosting] [negative_boost] must be set and in [0, 1]")
	}
	return q, nil
}

type boostingQuery struct {
	positive, negative   riot.Query
	negativeBoost, boost float64
}

func (q *boostingQuery) Boost() float64 { return q.boost }

func (q *boostingQuery) Searcher(i search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	return newRescoreSearcher(i, options, q.positive, []riot.Query{q.negative}, func(dm *search.DocumentMatch, matched []bool) {
		if matched[0] {
			dm.Score *= q.negativeBoost
		}
		dm.Score *= q.boost
	})
}

func (q *boostingQuery) Validate() error {
	return validateAll(q.positive, q.negative)
}

func validateAll(queries ...riot.Query) error {
	for _, q := range queries {
		if v, ok := q.(interface{ Validate() error }); ok {
			if err := v.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// rescoreSearcher wraps the searcher of an inner query and rewrites the score
// of every match, given which of the marker queries the match also matches.
type rescoreSearcher struct {
	search.Searcher
	markers []*filterCursor
	matched []bool
	rescore func(dm *search.DocumentMatch, matched []bool)
}

func newRescoreSearcher(i search.Reader, options search.SearcherOptions, inner riot.Query, markers []riot.Query,
	rescore func(dm *search.DocumentMatch, matched []bool)) (search.Searcher, error) {
	s, err := inner.Searcher(i, options)
	if err != nil {
		return nil, err
	}
	rs := &rescoreSearcher{Searcher: s, matched: make([]bool, len(markers)), rescore: rescore}
	markerOptions := options
	markerOptions.Score = "none"
	for _, m := range markers {
		ms, err := m.Searcher(i, markerOptions)
		if err != nil {
			if cerr := rs.Close(); cerr != nil {
				return nil, fmt.Errorf("%w; close searcher: %v", err, cerr)
			}
			return nil, err
		}
		rs.markers = append(rs.markers, &filterCursor{searcher: ms})
	}
	return rs, nil
}

func (s *rescoreSearcher) apply(ctx *search.Context, dm *search.DocumentMatch) (*search.DocumentMatch, error) {
	if dm == nil {
		return nil, nil
	}
	for i, m := range s.markers {
		ok, err := m.contains(ctx, dm.Number)
		if err != nil {
			return nil, err
		}
		s.matched[i] = ok
	}
	s.rescore(dm, s.matched)
	return dm, nil
}

func (s *rescoreSearcher) Next(ctx *search.Context) (*search.DocumentMatch, error) {
	dm, err := s.Searcher.Next(ctx)
	if err != nil {
		return nil, err
	}
	return s.apply(ctx, dm)
}

func (s *rescoreSearcher) Advance(ctx *search.Context, number uint64) (*search.DocumentMatch, error) {
	dm, err := s.Searcher.Advance(ctx, number)
	if err != nil {
		return nil, err
	}
	return s.apply(ctx, dm)
}

func (s *rescoreSearcher) Close() error {
	err := s.Searcher.Close()
	for _, m := range s.markers {
		if merr := m.searcher.Close(); merr != nil && err == nil {
			err = merr
		}
	}
	return err
}

func (s *rescoreSearcher) DocumentMatchPoolSize() int {
	size := s.Searcher.DocumentMatchPoolSize()
	for _, m := range s.markers {
		size += m.searcher.DocumentMatchPoolSize() + 1
	}
	return size
}
