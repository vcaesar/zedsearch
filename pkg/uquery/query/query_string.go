/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI.
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
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
or production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package query

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	querystr "github.com/vcaesar/query_string"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/analysis/analyzer"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	zedanalysis "github.com/vcaesar/zedsearch/pkg/uquery/analysis"
)

func QueryStringQuery(query map[string]interface{}, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) (riot.Query, error) {
	value := new(meta.QueryStringQuery)
	for k, v := range query {
		k := strings.ToLower(k)
		switch k {
		case "query":
			value.Query = v.(string)
		case "analyzer":
			value.Analyzer = v.(string)
		case "fields":
			if vv, ok := v.([]interface{}); ok {
				for _, vvv := range vv {
					value.Fields = append(value.Fields, vvv.(string))
				}
			}
		case "default_field":
			value.DefaultField = v.(string)
		case "default_operator":
			value.DefaultOperator = v.(string)
		case "boost":
			value.Boost = v.(float64)
		case "analyze_wildcard":
			// noop
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_string] unsupported children %s", k))
		}
	}

	options := querystr.DefaultOptions()

	// TODO fields
	// TODO default_field
	// TODO default_operator
	// TODO boost

	zer, _ := zedanalysis.QueryAnalyzer(analyzers, value.Analyzer)
	if zer == nil {
		zer = analyzer.NewStandardAnalyzer()
	}
	options.WithDefaultAnalyzer(zer)

	fields := value.Fields
	if len(value.Fields) == 0 {
		for field, prop := range mappings.ListProperty() {
			if prop.Type == "text" || prop.Type == "keyword" {
				value.Fields = append(value.Fields, field)
			}
		}
	}
	for _, field := range value.Fields {
		var zer *analysis.Analyzer
		prop, _ := mappings.GetProperty(field)
		if prop.Type == "keyword" {
			zer = analyzer.NewKeywordAnalyzer()
		} else {
			indexZer, searchZer := zedanalysis.QueryAnalyzerForField(analyzers, mappings, field)
			if zer == nil && searchZer != nil {
				zer = searchZer
			}
			if zer == nil && indexZer != nil {
				zer = indexZer
			}
		}
		if zer != nil {
			options.WithAnalyzerForField(field, zer)
		}
	}

	q, err := querystr.ParseQueryString(value.Query, options)
	if err != nil || mappings.AllEnabled() {
		return q, err
	}
	// without _all, terms without a field search the default fields, as
	// Elasticsearch index.query.default_field "*"
	if value.DefaultField != "" && value.DefaultField != "*" {
		fields = []string{value.DefaultField}
	}
	implicit := zer
	if value.Analyzer != "" {
		implicit = nil // the analyzer of the clauses was requested
	}
	return withDefaultFields(q, lazyDefaultFields(mappings, analyzers, fields, implicit)), nil
}

// defaultFieldSet holds the fields a query without a field searches, by type,
// and the analyzers of the term fields.
type defaultFieldSet struct {
	terms, numbers, dates []string
	keywords              map[string]bool
	analyzers             map[string]*analysis.Analyzer
	// implicit is the analyzer a query without a field gets when the request
	// names none; any other analyzer was requested and is kept.
	implicit *analysis.Analyzer
}

// lazyDefaultFields returns the default fields of mappings, or of fields when
// set, built on first use.
func lazyDefaultFields(mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer, fields []string, implicit *analysis.Analyzer) func() *defaultFieldSet {
	var set *defaultFieldSet
	return func() *defaultFieldSet {
		if set == nil {
			set = defaultFields(mappings, analyzers, fields)
			set.implicit = implicit
		}
		return set
	}
}

// defaultFields sorts the indexed fields by type: queries without a field
// search the terms (text, keyword), numbers or dates.
func defaultFields(mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer, fields []string) *defaultFieldSet {
	set := &defaultFieldSet{keywords: make(map[string]bool), analyzers: make(map[string]*analysis.Analyzer)}
	if len(fields) == 0 {
		for field := range mappings.ListProperty() {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	// a field named twice would score twice
	for _, field := range slices.Compact(fields) {
		prop, ok := mappings.GetProperty(field)
		switch {
		case !ok || !prop.Index || strings.HasPrefix(field, "_"):
		case prop.Type == "keyword":
			set.terms = append(set.terms, field)
			set.keywords[field] = true
			set.analyzers[field] = analyzer.NewKeywordAnalyzer()
		case prop.Type == "text":
			set.terms = append(set.terms, field)
			indexZer, searchZer := zedanalysis.QueryAnalyzerForField(analyzers, mappings, field)
			if searchZer == nil {
				searchZer = indexZer
			}
			if searchZer != nil {
				set.analyzers[field] = searchZer
			}
		case prop.Type == "numeric":
			set.numbers = append(set.numbers, field)
		case prop.Type == "date" || prop.Type == "time":
			set.dates = append(set.dates, field)
		}
	}
	return set
}

// withDefaultFields replaces the queries of q without a field, or on _all,
// with a disjunction of the query on each default field.
func withDefaultFields(q riot.Query, set func() *defaultFieldSet) riot.Query {
	// requested is the analyzer of the query, nil for queries without one
	expand := func(field string, requested *analysis.Analyzer, fields func(*defaultFieldSet) []string, build func(string, *analysis.Analyzer) riot.Query) riot.Query {
		if field != "" && field != "_all" {
			return q
		}
		s := set()
		if len(fields(s)) == 0 {
			return riot.NewMatchNoneQuery()
		}
		out := riot.NewBooleanQuery().SetMinShould(1)
		for _, f := range fields(s) {
			zer := s.analyzers[f]
			if requested != nil && requested != s.implicit {
				zer = requested
			}
			out.AddShould(build(f, zer))
		}
		return out
	}
	terms := func(s *defaultFieldSet) []string { return s.terms }
	switch v := q.(type) {
	case *riot.BooleanQuery:
		for _, clauses := range [][]riot.Query{v.Musts(), v.Shoulds(), v.MustNots()} {
			for i, c := range clauses {
				clauses[i] = withDefaultFields(c, set)
			}
		}
		return v
	case *riot.MatchQuery:
		return expand(v.Field(), v.Analyzer(), terms, func(f string, zer *analysis.Analyzer) riot.Query {
			c := *v
			if zer != nil {
				c.SetAnalyzer(zer)
			}
			return c.SetField(f)
		})
	case *riot.MatchPhraseQuery:
		return expand(v.Field(), v.Analyzer(), terms, func(f string, zer *analysis.Analyzer) riot.Query {
			if s := set(); s.keywords[f] {
				// keywords keep no positions, the whole value is one term
				return riot.NewMatchQuery(v.Phrase()).SetField(f).SetAnalyzer(zer).SetBoost(v.Boost())
			}
			c := *v
			if zer != nil {
				c.SetAnalyzer(zer)
			}
			return c.SetField(f)
		})
	case *riot.TermQuery:
		return expand(v.Field(), nil, terms, func(f string, _ *analysis.Analyzer) riot.Query { c := *v; return c.SetField(f) })
	case *riot.PrefixQuery:
		return expand(v.Field(), nil, terms, func(f string, _ *analysis.Analyzer) riot.Query { c := *v; return c.SetField(f) })
	case *riot.FuzzyQuery:
		return expand(v.Field(), nil, terms, func(f string, _ *analysis.Analyzer) riot.Query { c := *v; return c.SetField(f) })
	case *riot.WildcardQuery:
		return expand(v.Field(), nil, terms, func(f string, _ *analysis.Analyzer) riot.Query { c := *v; return c.SetField(f) })
	case *riot.RegexpQuery:
		return expand(v.Field(), nil, terms, func(f string, _ *analysis.Analyzer) riot.Query { c := *v; return c.SetField(f) })
	case *riot.NumericRangeQuery:
		return expand(v.Field(), nil, func(s *defaultFieldSet) []string { return s.numbers },
			func(f string, _ *analysis.Analyzer) riot.Query { c := *v; return c.SetField(f) })
	case *riot.DateRangeQuery:
		return expand(v.Field(), nil, func(s *defaultFieldSet) []string { return s.dates },
			func(f string, _ *analysis.Analyzer) riot.Query { c := *v; return c.SetField(f) })
	}
	return q
}
