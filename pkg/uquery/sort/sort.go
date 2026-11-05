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

package sort

import (
	"strings"

	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/errors"
)

func Request(v interface{}) (search.SortOrder, error) {
	if v == nil {
		return nil, nil
	}
	if v, ok := v.(search.SortOrder); ok {
		return v, nil
	}

	sorts := make(search.SortOrder, 0, 1)
	switch v := v.(type) {
	case string:
		sorts = append(sorts, parseSortString(v))
		return sorts, nil
	case []interface{}:
		for _, v := range v {
			switch v := v.(type) {
			case string:
				sorts = append(sorts, parseSortString(v))
			case map[string]interface{}:
				if len(v) > 1 {
					return nil, errors.New(errors.ErrorTypeParsingException, "[sort] field doesn't support multiple values")
				}
				for field, v := range v {
					sort := search.SortBy(FirstValue(field))
					switch v := v.(type) {
					case string:
						if strings.ToLower(v) == "desc" {
							sort.Desc()
						}
					case map[string]interface{}:
						for kk, vv := range v {
							kk = strings.ToLower(kk)
							switch kk {
							case "order":
								if strings.ToLower(vv.(string)) == "desc" {
									sort.Desc()
								}
							case "format":
							default:
							}
						}
					default:
					}
					sorts = append(sorts, sort)
				}
			}
		}
	default:
		return nil, errors.New(errors.ErrorTypeXContentParseException, "[sort] value should be string or array")
	}

	return sorts, nil
}

// parseSortString is search.ParseSearchSortString with a FirstValue source.
func parseSortString(input string) *search.Sort {
	desc := strings.HasPrefix(input, "-")
	input = strings.TrimPrefix(strings.TrimPrefix(input, "-"), "+")
	if input == "_score" {
		return search.SortBy(&search.ScoreSource{}).Desc()
	}
	rv := search.SortBy(FirstValue(input))
	if desc {
		rv.Desc()
	}
	return rv
}

// FirstValue is a sort / aggregation source returning the first doc value of
// a field. It returns what search.FieldSource.Value does, the first value,
// without the per-document slice that search.RemoveNumericPaddedTerms
// allocates, or the error it builds for every non-numeric term.
type FirstValue string

func (f FirstValue) Fields() []string { return []string{string(f)} }

// NumericField implements search.NumericFieldSource: for a numeric column
// with one value the first doc value is that value prefix-coded, so riot
// can reject hits by their typed value.
func (f FirstValue) NumericField() string { return string(f) }

func (f FirstValue) Value(match *search.DocumentMatch) []byte {
	if vals := match.DocValues(string(f)); len(vals) > 0 {
		return vals[0]
	}
	return nil
}

// AppendValue implements search.TextValueAppender; nil means missing.
func (f FirstValue) AppendValue(match *search.DocumentMatch, buf []byte) []byte {
	if v := f.Value(match); v != nil {
		return append(buf[:0], v...)
	}
	return nil
}
