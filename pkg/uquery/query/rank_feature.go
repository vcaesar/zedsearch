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

package query

import (
	"fmt"
	"math"
	"strings"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// RankFeatureQuery scores the documents holding a numeric field by a function
// of its value: saturation (default), log, sigmoid or linear. Without pivot,
// saturation uses pivot 1: zed keeps no per-field value statistics.
func RankFeatureQuery(query map[string]interface{}) (riot.Query, error) {
	q := &rankFeatureQuery{boost: 1}
	fn := ""
	var body map[string]interface{}
	for k, v := range query {
		key := strings.ToLower(k)
		switch key {
		case "field":
			field, ok := v.(string)
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[rank_feature] field doesn't support values of type: %T", v))
			}
			q.field = field
		case "boost":
			f, err := zutils.ToFloat64(v)
			if err != nil {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[rank_feature] boost doesn't support values of type: %T", v))
			}
			q.boost = f
		case "saturation", "log", "sigmoid", "linear":
			if fn != "" {
				return nil, errors.New(errors.ErrorTypeParsingException, "[rank_feature] supports only one of [saturation, log, sigmoid, linear]")
			}
			fn = key
			b, ok := v.(map[string]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[rank_feature] %s doesn't support values of type: %T", key, v))
			}
			body = b
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rank_feature] unknown field [%s]", k))
		}
	}
	if q.field == "" {
		return nil, errors.New(errors.ErrorTypeParsingException, "[rank_feature] requires [field]")
	}
	f, err := rankFeatureFunc(fn, body)
	if err != nil {
		return nil, err
	}
	q.fn = f
	return q, nil
}

func rankFeatureFunc(name string, body map[string]interface{}) (func(float64) float64, error) {
	param := func(key string, required bool, def float64) (float64, error) {
		v, ok := body[key]
		if !ok {
			if required {
				return 0, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rank_feature] %s requires [%s]", name, key))
			}
			return def, nil
		}
		f, err := zutils.ToFloat64(v)
		if err != nil || f <= 0 {
			return 0, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rank_feature] %s [%s] must be a positive number", name, key))
		}
		return f, nil
	}
	switch name {
	case "log":
		factor, err := param("scaling_factor", true, 0)
		if err != nil {
			return nil, err
		}
		return func(v float64) float64 { return math.Log(factor + v) }, nil
	case "sigmoid":
		pivot, err := param("pivot", true, 0)
		if err != nil {
			return nil, err
		}
		exp, err := param("exponent", true, 0)
		if err != nil {
			return nil, err
		}
		return func(v float64) float64 { return sigmoid(v, pivot, exp) }, nil
	case "linear":
		return func(v float64) float64 { return v }, nil
	default: // saturation
		pivot, err := param("pivot", false, 1)
		if err != nil {
			return nil, err
		}
		return func(v float64) float64 { return v / (v + pivot) }, nil
	}
}

// sigmoid is v^exp / (v^exp + pivot^exp), written as 1 / (1 + (pivot/v)^exp)
// so that large powers do not overflow to Inf/Inf. When pivot/v itself
// overflows or underflows, the power is taken in the log domain.
func sigmoid(v, pivot, exp float64) float64 {
	ratio := pivot / v
	if v > 0 && pivot > 0 && (ratio == 0 || math.IsInf(ratio, 0)) {
		return 1 / (1 + math.Exp(exp*(math.Log(pivot)-math.Log(v))))
	}
	return 1 / (1 + math.Pow(ratio, exp))
}

type rankFeatureQuery struct {
	field string
	boost float64
	fn    func(float64) float64
}

func (q *rankFeatureQuery) Boost() float64 { return q.boost }

func (q *rankFeatureQuery) Searcher(i search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	exists := riot.NewNumericRangeInclusiveQuery(riot.MinNumeric, riot.MaxNumeric, true, true).SetField(q.field)
	s, err := exists.Searcher(i, options)
	if err != nil {
		return nil, err
	}
	ss := &scoringSearcher{Searcher: s, reader: i, fields: []string{q.field}, score: q.score}
	ss.values, ss.valuesCtx = &search.DocumentMatch{}, search.NewSearchContext(0, 0)
	return ss, nil
}

func (q *rankFeatureQuery) score(dm, values *search.DocumentMatch) (bool, error) {
	numbers := search.Field(q.field).Numbers(values)
	if len(numbers) == 0 || numbers[0] <= 0 {
		return false, nil // Elasticsearch rank features are positive
	}
	score := q.fn(numbers[0]) * q.boost
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 {
		return false, nil
	}
	dm.Score = score
	return true, nil
}
