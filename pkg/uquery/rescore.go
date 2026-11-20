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

package uquery

import (
	"fmt"
	"math"
	"strings"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// DefaultRescoreWindow is the Elasticsearch default window_size.
const DefaultRescoreWindow = 10

// Rescorer is one query rescorer of the rescore section of a search.
type Rescorer struct {
	WindowSize         int
	Query              map[string]interface{}
	QueryWeight        float64
	RescoreQueryWeight float64
	ScoreMode          string // total, multiply, avg, max, min
}

// ParseRescore parses a rescorer object or an array of them.
func ParseRescore(v interface{}) ([]*Rescorer, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]interface{})
	if !ok {
		items = []interface{}{v}
	}
	rescorers := make([]*Rescorer, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] must be an object or an array of objects, got %T", item))
		}
		r, err := parseRescorer(m)
		if err != nil {
			return nil, err
		}
		rescorers = append(rescorers, r)
	}
	return rescorers, nil
}

func parseRescorer(m map[string]interface{}) (*Rescorer, error) {
	r := &Rescorer{WindowSize: DefaultRescoreWindow, QueryWeight: 1, RescoreQueryWeight: 1, ScoreMode: "total"}
	for k, v := range m {
		switch strings.ToLower(k) {
		case "window_size":
			n, err := zutils.ToInt(v)
			if err != nil || n < 0 {
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] window_size must be a non-negative integer, got %v", v))
			}
			r.WindowSize = n
		case "query":
			body, ok := v.(map[string]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] query doesn't support values of type: %T", v))
			}
			if err := parseRescoreQuery(r, body); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] unknown rescorer [%s]", k))
		}
	}
	if r.Query == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[rescore] requires [query] with [rescore_query]")
	}
	return r, nil
}

func parseRescoreQuery(r *Rescorer, body map[string]interface{}) error {
	for k, v := range body {
		key := strings.ToLower(k)
		switch key {
		case "rescore_query":
			q, ok := v.(map[string]interface{})
			if !ok {
				return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] rescore_query doesn't support values of type: %T", v))
			}
			r.Query = q
		case "query_weight", "rescore_query_weight":
			f, err := zutils.ToFloat64(v)
			if err != nil {
				return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] %s doesn't support values of type: %T", key, v))
			}
			if key == "query_weight" {
				r.QueryWeight = f
			} else {
				r.RescoreQueryWeight = f
			}
		case "score_mode":
			mode, err := zutils.ToString(v)
			mode = strings.ToLower(mode)
			switch {
			case err != nil:
				return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] score_mode doesn't support values of type: %T", v))
			case mode == "total", mode == "multiply", mode == "avg", mode == "max", mode == "min":
				r.ScoreMode = mode
			default:
				return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] unknown score_mode [%s]", mode))
			}
		default:
			return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rescore] unknown field [%s]", k))
		}
	}
	return nil
}

// Combine returns the rescored score of a hit of the window. matched is false
// when the hit does not match the rescore query, which keeps its weighted
// original score, as Elasticsearch does.
func (r *Rescorer) Combine(score, rescore float64, matched bool) float64 {
	primary := score * r.QueryWeight
	if !matched {
		return primary
	}
	secondary := rescore * r.RescoreQueryWeight
	switch r.ScoreMode {
	case "multiply":
		return primary * secondary
	case "avg":
		return (primary + secondary) / 2
	case "max":
		return math.Max(primary, secondary)
	case "min":
		return math.Min(primary, secondary)
	default:
		return primary + secondary
	}
}

// RescoreWindow is the number of top hits a search has to collect so that
// every rescorer sees its whole window and the requested page.
func RescoreWindow(rescorers []*Rescorer, from, size int) int {
	n := from + size
	for _, r := range rescorers {
		n = max(n, r.WindowSize)
	}
	return n
}
