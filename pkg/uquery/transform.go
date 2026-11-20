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

import "strings"

// TransformFunc replaces the body of a query clause by a new query.
type TransformFunc func(body map[string]interface{}) (map[string]interface{}, error)

// subQueryFields lists the fields of compound queries holding sub queries.
var subQueryFields = map[string][]string{
	"bool":           {"must", "should", "must_not", "filter"},
	"constant_score": {"filter"},
	"dis_max":        {"queries"},
	"boosting":       {"positive", "negative"},
	"function_score": {"query"},
	"script_score":   {"query"},
	"pinned":         {"organic"},
	"nested":         {"query"},
	"has_child":      {"query"},
	"has_parent":     {"query"},
}

// Transform walks query and replaces every clause named in fns, at any depth
// of compound queries, by the query its function returns.
func Transform(query interface{}, fns map[string]TransformFunc) (interface{}, error) {
	q, err := queryToMap(query)
	if err != nil || q == nil {
		return query, err
	}
	return transformQuery(q, fns)
}

func transformQuery(q map[string]interface{}, fns map[string]TransformFunc) (map[string]interface{}, error) {
	if len(q) == 1 {
		for k, v := range q {
			if fn, ok := fns[strings.ToLower(k)]; ok {
				body, ok := v.(map[string]interface{})
				if !ok {
					return q, nil // the query parser reports the bad type
				}
				return fn(body)
			}
		}
	}

	out := make(map[string]interface{}, len(q))
	for k, v := range q {
		out[k] = v
		fields, ok := subQueryFields[strings.ToLower(k)]
		clause, isMap := v.(map[string]interface{})
		if !ok || !isMap {
			continue
		}
		newClause := make(map[string]interface{}, len(clause))
		for ck, cv := range clause {
			newClause[ck] = cv
			if !containsFold(fields, ck) {
				continue
			}
			sub, err := transformClause(cv, fns)
			if err != nil {
				return nil, err
			}
			newClause[ck] = sub
		}
		if strings.EqualFold(k, "function_score") {
			if err := transformFunctions(newClause, fns); err != nil {
				return nil, err
			}
		}
		out[k] = newClause
	}
	return out, nil
}

func transformClause(v interface{}, fns map[string]TransformFunc) (interface{}, error) {
	switch v := v.(type) {
	case map[string]interface{}:
		return transformQuery(v, fns)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = item
			if sub, ok := item.(map[string]interface{}); ok {
				rewritten, err := transformQuery(sub, fns)
				if err != nil {
					return nil, err
				}
				out[i] = rewritten
			}
		}
		return out, nil
	}
	return v, nil
}

// transformFunctions transforms the filter of each function_score function.
func transformFunctions(clause map[string]interface{}, fns map[string]TransformFunc) error {
	functions, ok := clause["functions"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]interface{}, len(functions))
	for i, item := range functions {
		out[i] = item
		fn, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		filter, ok := fn["filter"].(map[string]interface{})
		if !ok {
			continue
		}
		rewritten, err := transformQuery(filter, fns)
		if err != nil {
			return err
		}
		newFn := make(map[string]interface{}, len(fn))
		for k, v := range fn {
			newFn[k] = v
		}
		newFn["filter"] = rewritten
		out[i] = newFn
	}
	clause["functions"] = out
	return nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
