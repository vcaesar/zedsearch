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
	"strconv"
	"strings"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/searcher"
	"github.com/vcaesar/riot/search/similarity"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

func TermQuery(query map[string]interface{}, mappings *meta.Mappings) (riot.Query, error) {
	if len(query) > 1 {
		return nil, errors.New(errors.ErrorTypeParsingException, "[term] query doesn't support multiple fields")
	}

	field := ""
	value := new(meta.TermQuery)
	value.Boost = -1.0
	for k, v := range query {
		field = k
		switch v := v.(type) {
		case string:
			value.Value = v
		case float64:
			value.Value = v
		case bool:
			value.Value = v
		case map[string]interface{}:
			for k, v := range v {
				k := strings.ToLower(k)
				switch k {
				case "value":
					value.Value = v
				case "case_insensitive":
					value.CaseInsensitive = v.(bool)
				case "boost":
					value.Boost = v.(float64)
				default:
					// return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[term] unknown field [%s]", k))
				}
			}
		default:
			return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[term] doesn't support values of type: %T", v))
		}
	}

	// TODO: case_insensitive support

	if field == "_index" {
		name, ok := value.Value.(string)
		if !ok {
			return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[term] _index doesn't support values of type: %T", value.Value))
		}
		q := &indexQuery{name: name}
		if value.Boost >= 0 {
			q.boost = &value.Boost
		}
		return q, nil
	}

	prop, _ := mappings.GetProperty(field)
	switch prop.Type {
	case "numeric":
		return TermQueryNumeric(field, value)
	case "bool":
		return TermQueryBool(field, value)
	default:
		return TermQueryText(field, value)
	}
}

func TermQueryNumeric(field string, value *meta.TermQuery) (riot.Query, error) {
	val, err := zutils.ToFloat64(value.Value)
	if err != nil {
		return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[term] convert value to numeric error: %s", err))
	}
	subq := riot.NewNumericRangeInclusiveQuery(val, val, true, true).SetField(field)
	if value.Boost >= 0 {
		subq.SetBoost(value.Boost)
	}
	return subq, nil
}

func TermQueryBool(field string, value *meta.TermQuery) (riot.Query, error) {
	val, err := zutils.ToBool(value.Value)
	if err != nil {
		return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[term] convert value to boolean error: %s", err))
	}
	subq := riot.NewTermQuery(strconv.FormatBool(val)).SetField(field)
	if value.Boost >= 0 {
		subq.SetBoost(value.Boost)
	}
	return subq, nil
}

func TermQueryText(field string, value *meta.TermQuery) (riot.Query, error) {
	val, err := zutils.ToString(value.Value)
	if err != nil {
		return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[term] convert value to string error: %s", err))
	}
	subq := riot.NewTermQuery(val).SetField(field)
	if value.Boost >= 0 {
		subq.SetBoost(value.Boost)
	}
	return subq, nil
}

// indexQuery matches the documents of the index name. _index is stored, not
// indexed, and a reader holds the documents of one index: the first document
// with an _index decides for all of them. Documents without _index match.
type indexQuery struct {
	name  string
	boost *float64 // nil scores neutrally
}

func (q *indexQuery) Searcher(i search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	// the match_all searcher ignores its boost, so the scorer carries it
	score := 1.0
	if q.boost != nil {
		score = *q.boost
	}
	s, err := searcher.NewMatchAllSearcher(i, score, similarity.ConstantScorer(score), options)
	if err != nil {
		return nil, err
	}
	return &indexSearcher{Searcher: s, reader: i, name: q.name}, nil
}

type indexSearcher struct {
	search.Searcher
	reader  search.Reader
	name    string
	checked bool // a document told the index of the reader
	other   bool // the reader is of another index
}

func (s *indexSearcher) filter(ctx *search.Context, dm *search.DocumentMatch, err error) (*search.DocumentMatch, error) {
	if err != nil || dm == nil {
		return nil, err
	}
	if !s.checked {
		err := s.reader.VisitStoredFields(dm.Number, func(field string, value []byte) bool {
			if field == "_index" {
				s.checked, s.other = true, string(value) != s.name
				return false
			}
			return true
		})
		if err != nil {
			return nil, err
		}
	}
	if s.other {
		ctx.DocumentMatchPool.Put(dm)
		return nil, nil
	}
	return dm, nil
}

func (s *indexSearcher) Next(ctx *search.Context) (*search.DocumentMatch, error) {
	dm, err := s.Searcher.Next(ctx)
	return s.filter(ctx, dm, err)
}

func (s *indexSearcher) Advance(ctx *search.Context, number uint64) (*search.DocumentMatch, error) {
	dm, err := s.Searcher.Advance(ctx, number)
	return s.filter(ctx, dm, err)
}
