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

package assoc

import (
	"fmt"
	"math"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

func paramError(kind, key string, v interface{}) error {
	return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] [%s] doesn't support values of type: %T", kind, key, v))
}

func paramInt(kind, key string, v interface{}) (int, error) {
	switch v := v.(type) {
	case bool:
		return 0, paramError(kind, key, v)
	case float64:
		// float64(math.MaxInt) rounds up to 2^63, which int cannot hold
		if v != math.Trunc(v) || v >= float64(math.MaxInt) || v < float64(math.MinInt) {
			return 0, paramError(kind, key, v)
		}
	}
	n, err := zutils.ToInt(v)
	if err != nil {
		return 0, paramError(kind, key, v)
	}
	return n, nil
}

func paramFloat(kind, key string, v interface{}) (float64, error) {
	if _, ok := v.(bool); ok {
		return 0, paramError(kind, key, v)
	}
	f, err := zutils.ToFloat64(v)
	if err != nil {
		return 0, paramError(kind, key, v)
	}
	return f, nil
}

func paramBool(kind, key string, v interface{}) (bool, error) {
	b, err := zutils.ToBool(v)
	if err != nil {
		return false, paramError(kind, key, v)
	}
	return b, nil
}

func paramString(kind, key string, v interface{}) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", paramError(kind, key, v)
	}
	return s, nil
}

// paramStrings reads a string or an array of strings.
func paramStrings(kind, key string, v interface{}) ([]string, error) {
	switch v := v.(type) {
	case string:
		return []string{v}, nil
	case []string:
		return v, nil
	case []interface{}:
		rv := make([]string, 0, len(v))
		for _, vv := range v {
			s, ok := vv.(string)
			if !ok {
				return nil, paramError(kind, key, vv)
			}
			rv = append(rv, s)
		}
		return rv, nil
	default:
		return nil, paramError(kind, key, v)
	}
}
