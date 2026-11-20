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

package meta

// QueryRuleset is a named list of query rules, as the Elasticsearch query
// rules API stores them.
type QueryRuleset struct {
	ID    string       `json:"ruleset_id"`
	Rules []*QueryRule `json:"rules"`
}

// QueryRule pins or excludes documents when all of its criteria match the
// match_criteria of a rule query.
type QueryRule struct {
	ID       string              `json:"rule_id"`
	Type     string              `json:"type"` // pinned, exclude
	Criteria []QueryRuleCriteria `json:"criteria"`
	Actions  QueryRuleActions    `json:"actions"`
	Priority *int                `json:"priority,omitempty"`
}

// QueryRuleCriteria compares the match_criteria value named metadata with
// values: exact, fuzzy, prefix, suffix, contains, lt, lte, gt, gte, always.
type QueryRuleCriteria struct {
	Type     string        `json:"type"`
	Metadata string        `json:"metadata,omitempty"`
	Values   []interface{} `json:"values,omitempty"`
}

// QueryRuleActions lists the documents a rule pins or excludes.
type QueryRuleActions struct {
	IDs  []string          `json:"ids,omitempty"`
	Docs []QueryRuleDocRef `json:"docs,omitempty"`
}

// QueryRuleDocRef is a document of an index.
type QueryRuleDocRef struct {
	Index string `json:"_index"`
	ID    string `json:"_id"`
}
