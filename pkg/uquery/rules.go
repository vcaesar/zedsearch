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
	"sort"
	"strings"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery/query"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// QueryRuleTypes and QueryRuleCriteriaTypes are the values the query rules
// API accepts.
var (
	QueryRuleTypes         = []string{"pinned", "exclude"}
	QueryRuleCriteriaTypes = []string{"exact", "fuzzy", "prefix", "suffix", "contains", "lt", "lte", "gt", "gte", "always"}
)

// ValidateQueryRule checks a rule of the query rules API.
func ValidateQueryRule(rule *meta.QueryRule) error {
	if rule == nil {
		return errors.New(errors.ErrorTypeParsingException, "[query_rule] must be an object")
	}
	if rule.ID == "" {
		return errors.New(errors.ErrorTypeParsingException, "[query_rule] requires [rule_id]")
	}
	if !containsFold(QueryRuleTypes, rule.Type) {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] unknown type [%s], expected one of %v", rule.Type, QueryRuleTypes))
	}
	if len(rule.Criteria) == 0 {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] [%s] requires [criteria]", rule.ID))
	}
	for _, c := range rule.Criteria {
		if !containsFold(QueryRuleCriteriaTypes, c.Type) {
			return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] unknown criteria type [%s], expected one of %v", c.Type, QueryRuleCriteriaTypes))
		}
		if strings.EqualFold(c.Type, "always") {
			continue
		}
		if c.Metadata == "" || len(c.Values) == 0 {
			return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] [%s] criteria [%s] requires [metadata] and [values]", rule.ID, c.Type))
		}
		if isNumericCriteria(c.Type) {
			for _, v := range c.Values {
				if _, err := zutils.ToFloat64(v); err != nil {
					return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] [%s] criteria [%s] values must be numbers", rule.ID, c.Type))
				}
			}
		}
	}
	if len(rule.Actions.IDs) > 0 && len(rule.Actions.Docs) > 0 {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] [%s] actions accept one of [ids] or [docs]", rule.ID))
	}
	if len(rule.Actions.IDs)+len(rule.Actions.Docs) == 0 {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] [%s] requires [actions] with [ids] or [docs]", rule.ID))
	}
	if len(rule.Actions.IDs)+len(rule.Actions.Docs) > query.MaxPinnedDocs {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] [%s] actions cannot hold more than %d documents", rule.ID, query.MaxPinnedDocs))
	}
	if rule.Priority != nil && *rule.Priority < 0 {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[query_rule] [%s] priority must be >= 0", rule.ID))
	}
	return nil
}

// SortQueryRules orders rules by priority, rules without priority last, and
// keeps the stored order of equal priorities.
func SortQueryRules(rules []*meta.QueryRule) {
	sort.SliceStable(rules, func(i, j int) bool {
		pi, pj := rules[i].Priority, rules[j].Priority
		if pi == nil || pj == nil {
			return pi != nil && pj == nil
		}
		return *pi < *pj
	})
}

// MatchQueryRules returns the rules of ruleset whose criteria all match.
func MatchQueryRules(ruleset *meta.QueryRuleset, criteria map[string]interface{}) []*meta.QueryRule {
	rules := append([]*meta.QueryRule(nil), ruleset.Rules...)
	SortQueryRules(rules)
	matched := make([]*meta.QueryRule, 0, len(rules))
	for _, rule := range rules {
		if ruleMatches(rule, criteria) {
			matched = append(matched, rule)
		}
	}
	return matched
}

func ruleMatches(rule *meta.QueryRule, criteria map[string]interface{}) bool {
	for _, c := range rule.Criteria {
		if !criteriaMatches(c, criteria) {
			return false
		}
	}
	return true
}

// criteriaMatches reports whether the match_criteria value named by c
// matches any of its values.
func criteriaMatches(c meta.QueryRuleCriteria, criteria map[string]interface{}) bool {
	typ := strings.ToLower(c.Type)
	if typ == "always" {
		return true
	}
	input, ok := criteria[c.Metadata]
	if !ok {
		return false
	}
	for _, want := range c.Values {
		if valueMatches(typ, input, want) {
			return true
		}
	}
	return false
}

func valueMatches(typ string, input, want interface{}) bool {
	if isNumericCriteria(typ) {
		a, err1 := zutils.ToFloat64(input)
		b, err2 := zutils.ToFloat64(want)
		if err1 != nil || err2 != nil {
			return false
		}
		switch typ {
		case "lt":
			return a < b
		case "lte":
			return a <= b
		case "gt":
			return a > b
		default:
			return a >= b
		}
	}
	a, err1 := zutils.ToString(input)
	b, err2 := zutils.ToString(want)
	if err1 != nil || err2 != nil {
		return false
	}
	switch typ {
	case "exact":
		return a == b
	case "prefix":
		return strings.HasPrefix(a, b)
	case "suffix":
		return strings.HasSuffix(a, b)
	case "contains":
		return strings.Contains(a, b)
	case "fuzzy":
		a, b = strings.ToLower(a), strings.ToLower(b)
		return editDistance(a, b) <= autoFuzziness(b)
	}
	return false
}

func isNumericCriteria(typ string) bool {
	switch strings.ToLower(typ) {
	case "lt", "lte", "gt", "gte":
		return true
	}
	return false
}

// autoFuzziness is the Elasticsearch AUTO fuzziness of term.
func autoFuzziness(term string) int {
	switch n := len([]rune(term)); {
	case n < 3:
		return 0
	case n < 6:
		return 1
	default:
		return 2
	}
}

// editDistance is the Levenshtein distance of a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// RulesetLookup loads a ruleset by id.
type RulesetLookup func(id string) (*meta.QueryRuleset, error)

// RuleQuery rewrites the body of a rule query into the query DSL applying the
// matched rules: pinned documents promote over organic, excluded documents
// are removed from all results.
func RuleQuery(body map[string]interface{}, lookup RulesetLookup) (map[string]interface{}, error) {
	var organic interface{}
	var ids []string
	var criteria map[string]interface{}
	for k, v := range body {
		switch strings.ToLower(k) {
		case "organic":
			organic = v
		case "match_criteria":
			m, ok := v.(map[string]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[rule] match_criteria doesn't support values of type: %T", v))
			}
			criteria = m
		case "ruleset_ids", "ruleset_id":
			switch v := v.(type) {
			case string:
				ids = append(ids, v)
			case []interface{}:
				for _, item := range v {
					id, ok := item.(string)
					if !ok {
						return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[rule] ruleset_ids doesn't support values of type: %T", item))
					}
					ids = append(ids, id)
				}
			default:
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[rule] ruleset_ids doesn't support values of type: %T", v))
			}
		case "boost", "_name":
			// accepted
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rule] unknown field [%s]", k))
		}
	}
	if organic == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[rule] requires [organic] query")
	}
	if len(ids) == 0 {
		return nil, errors.New(errors.ErrorTypeParsingException, "[rule] requires [ruleset_ids]")
	}
	if criteria == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[rule] requires [match_criteria]")
	}

	var pinned, excluded []string
	for _, id := range ids {
		ruleset, err := lookup(id)
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[rule] ruleset [%s] not found", id))
		}
		for _, rule := range MatchQueryRules(ruleset, criteria) {
			docs := ruleDocIDs(rule)
			if strings.EqualFold(rule.Type, "exclude") {
				excluded = append(excluded, docs...)
			} else {
				pinned = append(pinned, docs...)
			}
		}
	}
	return ruleResult(organic, pinned, excluded), nil
}

func ruleDocIDs(rule *meta.QueryRule) []string {
	ids := append([]string(nil), rule.Actions.IDs...)
	for _, d := range rule.Actions.Docs {
		ids = append(ids, d.ID)
	}
	return ids
}

// ruleResult builds pinned{ids, organic} with excluded ids removed from both.
func ruleResult(organic interface{}, pinned, excluded []string) map[string]interface{} {
	if len(excluded) > 0 {
		drop := make(map[string]struct{}, len(excluded))
		values := make([]interface{}, 0, len(excluded))
		for _, id := range excluded {
			drop[id] = struct{}{}
			values = append(values, id)
		}
		kept := pinned[:0]
		for _, id := range pinned {
			if _, ok := drop[id]; !ok {
				kept = append(kept, id)
			}
		}
		pinned = kept
		organic = map[string]interface{}{"bool": map[string]interface{}{
			"must":     organic,
			"must_not": map[string]interface{}{"ids": map[string]interface{}{"values": values}},
		}}
	}
	pinned = unique(pinned)
	if len(pinned) > query.MaxPinnedDocs {
		pinned = pinned[:query.MaxPinnedDocs]
	}
	if len(pinned) == 0 {
		if m, ok := organic.(map[string]interface{}); ok {
			return m
		}
		return map[string]interface{}{"bool": map[string]interface{}{"must": organic}}
	}
	values := make([]interface{}, len(pinned))
	for i, id := range pinned {
		values[i] = id
	}
	return map[string]interface{}{"pinned": map[string]interface{}{"ids": values, "organic": organic}}
}
