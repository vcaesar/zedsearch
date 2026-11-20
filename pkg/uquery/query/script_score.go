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
 */

package query

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// scriptDocField finds the doc['field'] references of a script.
var scriptDocField = regexp.MustCompile(`doc\s*\[\s*['"]([^'"]+)['"]\s*\]`)

// scriptDocEmpty finds the doc['field'].empty checks of a script.
var scriptDocEmpty = regexp.MustCompile(`(doc\s*\[\s*['"][^'"]+['"]\s*\])\.empty\b`)

// scriptFunctions are the painless score functions and Math helpers a
// script_score script may call; scripts are evaluated with expr, so the
// painless subset `_score`, `doc['f'].value`, `doc['f'].size()`, `params.x`
// and these calls are supported.
var scriptFunctions = map[string]interface{}{
	"saturation": func(v, k interface{}) float64 { return num(v) / (num(k) + num(v)) },
	"sigmoid": func(v, k, a interface{}) float64 {
		return sigmoid(num(v), num(k), num(a))
	},
	"Math": map[string]interface{}{
		"log":   func(v interface{}) float64 { return math.Log(num(v)) },
		"log10": func(v interface{}) float64 { return math.Log10(num(v)) },
		"log1p": func(v interface{}) float64 { return math.Log1p(num(v)) },
		"sqrt":  func(v interface{}) float64 { return math.Sqrt(num(v)) },
		"exp":   func(v interface{}) float64 { return math.Exp(num(v)) },
		"abs":   func(v interface{}) float64 { return math.Abs(num(v)) },
		"pow":   func(v, e interface{}) float64 { return math.Pow(num(v), num(e)) },
		"min":   func(a, b interface{}) float64 { return math.Min(num(a), num(b)) },
		"max":   func(a, b interface{}) float64 { return math.Max(num(a), num(b)) },
	},
}

// num converts a script value to float64: expr literals may be ints, and a
// value that is not a number is NaN, which the score check rejects.
func num(v interface{}) float64 {
	f, err := zutils.ToFloat64(v)
	if err != nil {
		return math.NaN()
	}
	return f
}

// ScriptScoreQuery scores the matches of query with a script.
func ScriptScoreQuery(query map[string]interface{}, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) (riot.Query, error) {
	q := &scriptScoreQuery{boost: 1}
	var source string
	params := map[string]interface{}{}
	for k, v := range query {
		key := strings.ToLower(k)
		switch key {
		case "query":
			vv, ok := v.(map[string]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[script_score] query doesn't support values of type: %T", v))
			}
			subq, err := Query(vv, mappings, analyzers)
			if err != nil {
				return nil, errors.New(errors.ErrorTypeParsingException, "[script_score] failed to parse [query]").Cause(err)
			}
			q.inner = subq
		case "script":
			switch s := v.(type) {
			case string:
				source = s
			case map[string]interface{}:
				source, _ = s["source"].(string)
				if p, ok := s["params"].(map[string]interface{}); ok {
					params = p
				}
			}
			if source == "" {
				return nil, errors.New(errors.ErrorTypeParsingException, `[script_score] script must be a string or {"source": "..."}`)
			}
		case "min_score", "boost":
			f, err := zutils.ToFloat64(v)
			if err != nil {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[script_score] %s doesn't support values of type: %T", key, v))
			}
			if key == "boost" {
				q.boost = f
			} else {
				q.minScore, q.hasMinScore = f, true
			}
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[script_score] unknown field [%s]", k))
		}
	}
	if q.inner == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[script_score] requires [query]")
	}
	if source == "" {
		return nil, errors.New(errors.ErrorTypeParsingException, "[script_score] requires [script]")
	}
	for _, m := range scriptDocField.FindAllStringSubmatch(source, -1) {
		q.fields = appendUnique(q.fields, m[1])
	}
	program, err := expr.Compile(painlessToExpr(source))
	if err != nil {
		return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[script_score] script compile err %s", err.Error()))
	}
	q.program, q.params = program, params
	return q, nil
}

// painlessToExpr rewrites the painless method calls expr has no syntax for;
// .empty is parenthesized so that operators like ! apply to the whole check.
func painlessToExpr(source string) string {
	source = scriptDocEmpty.ReplaceAllString(source, "($1.size == 0)")
	return strings.ReplaceAll(source, ".size()", ".size")
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

type scriptScoreQuery struct {
	inner       riot.Query
	program     *vm.Program
	params      map[string]interface{}
	fields      []string
	boost       float64
	minScore    float64
	hasMinScore bool
}

func (q *scriptScoreQuery) Boost() float64 { return q.boost }

func (q *scriptScoreQuery) Validate() error { return validateAll(q.inner) }

func (q *scriptScoreQuery) Searcher(i search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	s, err := q.inner.Searcher(i, options)
	if err != nil {
		return nil, err
	}
	ss := &scoringSearcher{Searcher: s, reader: i, fields: q.fields, score: q.score}
	if len(q.fields) > 0 {
		ss.values, ss.valuesCtx = &search.DocumentMatch{}, search.NewSearchContext(0, 0)
	}
	return ss, nil
}

func (q *scriptScoreQuery) score(dm, values *search.DocumentMatch) (bool, error) {
	doc := make(map[string]interface{}, len(q.fields))
	for _, f := range q.fields {
		doc[f] = docField(values, f)
	}
	env := map[string]interface{}{"_score": dm.Score, "doc": doc, "params": q.params}
	for k, v := range scriptFunctions {
		env[k] = v
	}
	out, err := expr.Run(q.program, env)
	if err != nil {
		return false, errors.New(errors.ErrorTypeRuntimeException, fmt.Sprintf("[script_score] script run err %s", err.Error()))
	}
	score, err := zutils.ToFloat64(out)
	if err != nil {
		return false, errors.New(errors.ErrorTypeRuntimeException, fmt.Sprintf("[script_score] script must return a number, got %T", out))
	}
	score *= q.boost
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 {
		return false, errors.New(errors.ErrorTypeRuntimeException,
			fmt.Sprintf("[script_score] script score [%v] must be a finite non-negative number", score))
	}
	if q.hasMinScore && score < q.minScore {
		return false, nil
	}
	dm.Score = score
	return true, nil
}

// docField is the painless doc['f'] of a match: value is the first number,
// or the first term for non numeric fields.
func docField(dm *search.DocumentMatch, field string) map[string]interface{} {
	source := search.Field(field)
	if numbers := source.Numbers(dm); len(numbers) > 0 {
		values := make([]interface{}, len(numbers))
		for i, n := range numbers {
			values[i] = n
		}
		return map[string]interface{}{"value": numbers[0], "values": values, "size": len(numbers)}
	}
	terms := search.RemoveNumericPaddedTerms(source.Values(dm))
	if len(terms) == 0 {
		return map[string]interface{}{"value": 0.0, "values": []interface{}{}, "size": 0}
	}
	values := make([]interface{}, len(terms))
	for i, t := range terms {
		values[i] = string(t)
	}
	return map[string]interface{}{"value": values[0], "values": values, "size": len(terms)}
}

// scoringSearcher rewrites the score of each match of the wrapped searcher,
// dropping the match when score returns false. The fields score reads are
// loaded through a private context, as functionScoreSearcher explains.
type scoringSearcher struct {
	search.Searcher
	reader    search.Reader
	fields    []string
	values    *search.DocumentMatch
	valuesCtx *search.Context
	score     func(dm, values *search.DocumentMatch) (bool, error)
}

func (s *scoringSearcher) rescore(dm *search.DocumentMatch) (bool, error) {
	values := dm
	if s.values != nil {
		values = s.values.Reset()
		values.SetReader(s.reader)
		values.Number = dm.Number
		if err := values.LoadDocumentValues(s.valuesCtx, s.fields); err != nil {
			return false, err
		}
	}
	return s.score(dm, values)
}

func (s *scoringSearcher) Next(ctx *search.Context) (*search.DocumentMatch, error) {
	for {
		dm, err := s.Searcher.Next(ctx)
		if err != nil || dm == nil {
			return nil, err
		}
		keep, err := s.rescore(dm)
		if err != nil {
			return nil, err
		}
		if keep {
			return dm, nil
		}
		ctx.DocumentMatchPool.Put(dm)
	}
}

func (s *scoringSearcher) Advance(ctx *search.Context, number uint64) (*search.DocumentMatch, error) {
	dm, err := s.Searcher.Advance(ctx, number)
	if err != nil || dm == nil {
		return nil, err
	}
	keep, err := s.rescore(dm)
	if err != nil {
		return nil, err
	}
	if keep {
		return dm, nil
	}
	ctx.DocumentMatchPool.Put(dm)
	return s.Next(ctx)
}

func (s *scoringSearcher) DocumentMatchPoolSize() int {
	return s.Searcher.DocumentMatchPoolSize() + 1
}
