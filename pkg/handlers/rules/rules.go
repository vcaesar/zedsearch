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

package rules

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/uquery"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

const (
	errorTypeNotFound = "resource_not_found_exception"
	errorTypeIllegal  = "illegal_argument_exception"
	// MaxRules is the Elasticsearch default limit of rules per ruleset.
	MaxRules = 100
)

// lock serializes the read-modify-write updates of rulesets.
var lock sync.Mutex

// ResultResponse is the response of a ruleset or rule change.
type ResultResponse struct {
	Result string `json:"result"`
}

// RulesetSummary is a ruleset of the list response.
type RulesetSummary struct {
	RulesetID               string         `json:"ruleset_id"`
	RuleTotalCount          int            `json:"rule_total_count"`
	RuleCriteriaTypesCounts map[string]int `json:"rule_criteria_types_counts"`
	RuleTypeCounts          map[string]int `json:"rule_type_counts"`
}

// ListResponse is a page of the rulesets.
type ListResponse struct {
	Count   int              `json:"count"`
	Results []RulesetSummary `json:"results"`
}

// MatchedRule is a rule matched by the test API.
type MatchedRule struct {
	RulesetID string `json:"ruleset_id"`
	RuleID    string `json:"rule_id"`
}

// TestRequest is the body of the test API; the match_criteria keys are free-form.
type TestRequest struct {
	MatchCriteria map[string]interface{} `json:"match_criteria" binding:"required"`
}

// TestResponse is the response of the test API.
type TestResponse struct {
	TotalMatchedRules int           `json:"total_matched_rules"`
	MatchedRules      []MatchedRule `json:"matched_rules"`
}

// ListRulesets lists the query rulesets sorted by id
//
// @Id ListQueryRulesets
// @Summary List query rulesets for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Produce json
// @Param   from query int false "Starting offset"
// @Param   size query int false "Max results, default 100"
// @Success 200 {object} rules.ListResponse
// @Failure 400 {object} map[string]interface{}
// @Router /es/_query_rules [get]
func ListRulesets(c zhttp.Context) {
	from, size, ok := page(c, "100")
	if !ok {
		return
	}
	rulesets, err := metadata.QueryRules.List(0, 0)
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	sort.Slice(rulesets, func(i, j int) bool { return rulesets[i].ID < rulesets[j].ID })
	results := make([]RulesetSummary, 0)
	if from < len(rulesets) {
		for _, rs := range rulesets[from : from+min(size, len(rulesets)-from)] {
			results = append(results, summary(rs))
		}
	}
	zutils.RenderJSON(c, http.StatusOK, ListResponse{Count: len(rulesets), Results: results})
}

func summary(rs *meta.QueryRuleset) RulesetSummary {
	s := RulesetSummary{RulesetID: rs.ID, RuleTotalCount: len(rs.Rules),
		RuleCriteriaTypesCounts: map[string]int{}, RuleTypeCounts: map[string]int{}}
	for _, rule := range rs.Rules {
		s.RuleTypeCounts[rule.Type]++
		for _, c := range rule.Criteria {
			s.RuleCriteriaTypesCounts[c.Type]++
		}
	}
	return s
}

// GetRuleset returns a query ruleset
//
// @Id GetQueryRuleset
// @Summary Get query ruleset for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Produce json
// @Param   id path string true "Ruleset id"
// @Success 200 {object} meta.QueryRuleset
// @Failure 404 {object} map[string]interface{}
// @Router /es/_query_rules/{id} [get]
func GetRuleset(c zhttp.Context) {
	rs, ok := loadRuleset(c, c.Param("ruleset_id"))
	if !ok {
		return
	}
	zutils.RenderJSON(c, http.StatusOK, rs)
}

// PutRuleset creates or replaces a query ruleset
//
// @Id PutQueryRuleset
// @Summary Create or update query ruleset for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Accept  json
// @Produce json
// @Param   id      path string            true "Ruleset id"
// @Param   ruleset body meta.QueryRuleset true "Rules"
// @Success 200 {object} rules.ResultResponse "Updated"
// @Success 201 {object} rules.ResultResponse "Created"
// @Failure 400 {object} map[string]interface{}
// @Router /es/_query_rules/{id} [put]
func PutRuleset(c zhttp.Context) {
	id := c.Param("ruleset_id")
	body := new(meta.QueryRuleset)
	if err := zutils.BindJSON(c, body); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}
	if len(body.Rules) == 0 {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, "[rules] must not be empty")
		return
	}
	if len(body.Rules) > MaxRules {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, fmt.Sprintf("[rules] cannot hold more than %d rules", MaxRules))
		return
	}
	seen := make(map[string]bool, len(body.Rules))
	for _, rule := range body.Rules {
		if err := uquery.ValidateQueryRule(rule); err != nil {
			renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
			return
		}
		if seen[rule.ID] {
			renderError(c, http.StatusBadRequest, errorTypeIllegal, fmt.Sprintf("duplicate rule_id [%s]", rule.ID))
			return
		}
		seen[rule.ID] = true
	}

	lock.Lock()
	defer lock.Unlock()
	existing, err := getRuleset(id)
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	body.ID = id
	save(c, body, existing == nil)
}

// DeleteRuleset deletes a query ruleset
//
// @Id DeleteQueryRuleset
// @Summary Delete query ruleset for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Produce json
// @Param   id path string true "Ruleset id"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /es/_query_rules/{id} [delete]
func DeleteRuleset(c zhttp.Context) {
	lock.Lock()
	defer lock.Unlock()
	id := c.Param("ruleset_id")
	if _, ok := loadRuleset(c, id); !ok {
		return
	}
	if err := metadata.QueryRules.Delete(id); err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	zutils.RenderJSON(c, http.StatusOK, zhttp.H{"acknowledged": true})
}

// GetRule returns a rule of a query ruleset
//
// @Id GetQueryRule
// @Summary Get query rule for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Produce json
// @Param   id      path string true "Ruleset id"
// @Param   rule_id path string true "Rule id"
// @Success 200 {object} meta.QueryRule
// @Failure 404 {object} map[string]interface{}
// @Router /es/_query_rules/{id}/_rule/{rule_id} [get]
func GetRule(c zhttp.Context) {
	rs, i, ok := loadRule(c)
	if !ok {
		return
	}
	zutils.RenderJSON(c, http.StatusOK, rs.Rules[i])
}

// PutRule creates or replaces a rule, creating its ruleset when missing
//
// @Id PutQueryRule
// @Summary Create or update query rule for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Accept  json
// @Produce json
// @Param   id      path string         true "Ruleset id"
// @Param   rule_id path string         true "Rule id"
// @Param   rule    body meta.QueryRule true "Rule"
// @Success 200 {object} rules.ResultResponse "Updated"
// @Success 201 {object} rules.ResultResponse "Created"
// @Failure 400 {object} map[string]interface{}
// @Router /es/_query_rules/{id}/_rule/{rule_id} [put]
func PutRule(c zhttp.Context) {
	rule := new(meta.QueryRule)
	if err := zutils.BindJSON(c, rule); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}
	rule.ID = c.Param("rule_id")
	if err := uquery.ValidateQueryRule(rule); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}

	lock.Lock()
	defer lock.Unlock()
	id := c.Param("ruleset_id")
	rs, err := getRuleset(id)
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	if rs == nil {
		rs = &meta.QueryRuleset{ID: id}
	}
	created := true
	for i, r := range rs.Rules {
		if r.ID == rule.ID {
			rs.Rules[i], created = rule, false
			break
		}
	}
	if created {
		if len(rs.Rules) >= MaxRules {
			renderError(c, http.StatusBadRequest, errorTypeIllegal, fmt.Sprintf("[rules] cannot hold more than %d rules", MaxRules))
			return
		}
		rs.Rules = append(rs.Rules, rule)
	}
	save(c, rs, created)
}

// DeleteRule deletes a rule of a query ruleset
//
// @Id DeleteQueryRule
// @Summary Delete query rule for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Produce json
// @Param   id      path string true "Ruleset id"
// @Param   rule_id path string true "Rule id"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /es/_query_rules/{id}/_rule/{rule_id} [delete]
func DeleteRule(c zhttp.Context) {
	lock.Lock()
	defer lock.Unlock()
	rs, i, ok := loadRule(c)
	if !ok {
		return
	}
	rs.Rules = append(rs.Rules[:i], rs.Rules[i+1:]...)
	var err error
	if len(rs.Rules) == 0 {
		err = metadata.QueryRules.Delete(rs.ID) // Elasticsearch drops an empty ruleset
	} else {
		err = metadata.QueryRules.Set(rs.ID, *rs)
	}
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	zutils.RenderJSON(c, http.StatusOK, zhttp.H{"acknowledged": true})
}

// TestRuleset returns the rules of a ruleset matching match_criteria
//
// @Id TestQueryRuleset
// @Summary Test query ruleset for compatible ES
// @security BasicAuth
// @Tags    QueryRules
// @Accept  json
// @Produce json
// @Param   id   path string                 true "Ruleset id"
// @Param   body body rules.TestRequest true "Criteria to match, e.g. {\"match_criteria\":{\"query_string\":\"boots\"}}"
// @Success 200 {object} rules.TestResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /es/_query_rules/{id}/_test [post]
func TestRuleset(c zhttp.Context) {
	var body TestRequest
	if err := zutils.BindJSON(c, &body); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}
	if body.MatchCriteria == nil {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, "[match_criteria] is required")
		return
	}
	rs, ok := loadRuleset(c, c.Param("ruleset_id"))
	if !ok {
		return
	}
	matched := make([]MatchedRule, 0)
	for _, rule := range uquery.MatchQueryRules(rs, body.MatchCriteria) {
		matched = append(matched, MatchedRule{RulesetID: rs.ID, RuleID: rule.ID})
	}
	zutils.RenderJSON(c, http.StatusOK, TestResponse{TotalMatchedRules: len(matched), MatchedRules: matched})
}

func save(c zhttp.Context, rs *meta.QueryRuleset, created bool) {
	if err := metadata.QueryRules.Set(rs.ID, *rs); err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	if created {
		zutils.RenderJSON(c, http.StatusCreated, ResultResponse{Result: "created"})
		return
	}
	zutils.RenderJSON(c, http.StatusOK, ResultResponse{Result: "updated"})
}

// getRuleset returns the stored ruleset id, nil when it does not exist.
func getRuleset(id string) (*meta.QueryRuleset, error) {
	rs, err := metadata.QueryRules.Get(id)
	if err != nil {
		if errors.Is(err, errors.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return rs, nil
}

func loadRuleset(c zhttp.Context, id string) (*meta.QueryRuleset, bool) {
	rs, err := getRuleset(id)
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return nil, false
	}
	if rs == nil {
		renderError(c, http.StatusNotFound, errorTypeNotFound, fmt.Sprintf("query ruleset [%s] not found", id))
		return nil, false
	}
	return rs, true
}

func loadRule(c zhttp.Context) (*meta.QueryRuleset, int, bool) {
	rs, ok := loadRuleset(c, c.Param("ruleset_id"))
	if !ok {
		return nil, 0, false
	}
	ruleID := c.Param("rule_id")
	for i, r := range rs.Rules {
		if r.ID == ruleID {
			return rs, i, true
		}
	}
	renderError(c, http.StatusNotFound, errorTypeNotFound, fmt.Sprintf("query rule [%s] not found in ruleset [%s]", ruleID, rs.ID))
	return nil, 0, false
}

func page(c zhttp.Context, defaultSize string) (int, int, bool) {
	from, err := strconv.Atoi(c.DefaultQuery("from", "0"))
	if err != nil || from < 0 {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, "[from] must be a non-negative integer")
		return 0, 0, false
	}
	size, err := strconv.Atoi(c.DefaultQuery("size", defaultSize))
	if err != nil || size < 0 {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, "[size] must be a non-negative integer")
		return 0, 0, false
	}
	return from, size, true
}

func renderError(c zhttp.Context, code int, typ, reason string) {
	zutils.RenderJSON(c, code, zhttp.H{
		"error":  zhttp.H{"type": typ, "reason": reason},
		"status": code,
	})
}
