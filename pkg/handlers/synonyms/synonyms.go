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

// Package synonyms serves the Elasticsearch compatible synonyms sets API.
package synonyms

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/ider"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	zedanalysis "github.com/vcaesar/zedsearch/pkg/uquery/analysis"
	zedtoken "github.com/vcaesar/zedsearch/pkg/uquery/analysis/token"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

const (
	errorTypeNotFound = "resource_not_found_exception"
	errorTypeIllegal  = "illegal_argument_exception"
)

// lock serializes the read-modify-write updates of synonyms sets.
var lock sync.Mutex

// Shards counts the indexes whose analyzers were reloaded.
type Shards struct {
	Total      int `json:"total"`
	Successful int `json:"successful"`
	Failed     int `json:"failed"`
}

// ReloadDetail lists the reloaded analyzers of an index.
type ReloadDetail struct {
	Index             string   `json:"index"`
	ReloadedAnalyzers []string `json:"reloaded_analyzers"`
	ReloadedNodeIDs   []string `json:"reloaded_node_ids"`
}

// ReloadDetails is the analyzers reload report of a change.
type ReloadDetails struct {
	Shards        Shards         `json:"_shards"`
	ReloadDetails []ReloadDetail `json:"reload_details"`
}

// UpdateResponse is the response of a synonyms set or rule change.
type UpdateResponse struct {
	Result                 string        `json:"result"`
	ReloadAnalyzersDetails ReloadDetails `json:"reload_analyzers_details"`
}

// SetResponse is a page of the rules of a synonyms set.
type SetResponse struct {
	Count       int                `json:"count"`
	SynonymsSet []meta.SynonymRule `json:"synonyms_set"`
}

// SetSummary is a synonyms set and its rules count.
type SetSummary struct {
	SynonymsSet string `json:"synonyms_set"`
	Count       int    `json:"count"`
}

// ListResponse is a page of the synonyms sets.
type ListResponse struct {
	Count   int          `json:"count"`
	Results []SetSummary `json:"results"`
}

// ListSets lists the synonyms sets sorted by id
//
// @Id ListSynonymsSets
// @Summary List synonyms sets for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Produce json
// @Param   from query int false "Starting offset" minimum(0)
// @Param   size query int false "Max results, default 10" minimum(0)
// @Success 200 {object} synonyms.ListResponse
// @Failure 400 {object} map[string]interface{}
// @Router /es/_synonyms [get]
func ListSets(c zhttp.Context) {
	from, size, ok := page(c)
	if !ok {
		return
	}
	sets, err := metadata.Synonyms.List(0, 0)
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].ID < sets[j].ID })

	pageSets := paginate(sets, from, size)
	results := make([]SetSummary, 0, len(pageSets))
	for _, set := range pageSets {
		results = append(results, SetSummary{SynonymsSet: set.ID, Count: len(set.Rules)})
	}
	zutils.RenderJSON(c, http.StatusOK, ListResponse{Count: len(sets), Results: results})
}

// GetSet returns a page of the rules of a synonyms set
//
// @Id GetSynonymsSet
// @Summary Get synonyms set for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Produce json
// @Param   id   path  string true  "Synonyms set id"
// @Param   from query int    false "Starting offset" minimum(0)
// @Param   size query int    false "Max results, default 10" minimum(0)
// @Success 200 {object} synonyms.SetResponse
// @Failure 400 {object} map[string]interface{} "Negative or non-integer from or size"
// @Failure 404 {object} map[string]interface{}
// @Router /es/_synonyms/{id} [get]
func GetSet(c zhttp.Context) {
	from, size, ok := page(c)
	if !ok {
		return
	}
	set, ok := loadSet(c, c.Param("set_id"))
	if !ok {
		return
	}
	if set == nil {
		renderSetNotFound(c, c.Param("set_id"))
		return
	}
	zutils.RenderJSON(c, http.StatusOK, SetResponse{
		Count:       len(set.Rules),
		SynonymsSet: append([]meta.SynonymRule{}, paginate(set.Rules, from, size)...),
	})
}

// PutSet creates or replaces a synonyms set and reloads the analyzers using it
//
// @Id PutSynonymsSet
// @Summary Create or update synonyms set for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Accept  json
// @Produce json
// @Param   id   path string          true "Synonyms set id"
// @Param   data body meta.SynonymSet true "Synonyms set"
// @Success 200 {object} synonyms.UpdateResponse
// @Success 201 {object} synonyms.UpdateResponse
// @Failure 400 {object} map[string]interface{}
// @Router /es/_synonyms/{id} [put]
func PutSet(c zhttp.Context) {
	var set meta.SynonymSet
	if err := zutils.BindJSON(c, &set); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}
	if err := prepareRules(set.Rules); err != nil {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, err.Error())
		return
	}
	set.ID = c.Param("set_id")

	lock.Lock()
	defer lock.Unlock()
	old, ok := loadSet(c, set.ID)
	if !ok {
		return
	}
	saveSet(c, &set, old == nil)
}

// DeleteSet deletes a synonyms set not used by any index
//
// @Id DeleteSynonymsSet
// @Summary Delete synonyms set for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Produce json
// @Param   id path string true "Synonyms set id"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /es/_synonyms/{id} [delete]
func DeleteSet(c zhttp.Context) {
	id := c.Param("set_id")

	lock.Lock()
	defer lock.Unlock()
	set, ok := loadSet(c, id)
	if !ok {
		return
	}
	if set == nil {
		renderSetNotFound(c, id)
		return
	}
	used, err := core.DeleteSynonymSet(id)
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	if len(used) > 0 {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, fmt.Sprintf(
			"synonyms set [%s] cannot be deleted as it is used by the indices [%s]", id, strings.Join(used, ", ")))
		return
	}
	zutils.RenderJSON(c, http.StatusOK, zhttp.H{"acknowledged": true})
}

// GetRule returns a synonym rule of a synonyms set
//
// @Id GetSynonymRule
// @Summary Get synonym rule for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Produce json
// @Param   id   path string true "Synonyms set id"
// @Param   rule path string true "Synonym rule id"
// @Success 200 {object} meta.SynonymRule
// @Failure 404 {object} map[string]interface{}
// @Router /es/_synonyms/{id}/{rule} [get]
func GetRule(c zhttp.Context) {
	set, i, ok := loadRule(c)
	if !ok {
		return
	}
	zutils.RenderJSON(c, http.StatusOK, set.Rules[i])
}

// PutRule creates or updates a synonym rule, creating the synonyms set when
// missing, and reloads the analyzers using it
//
// @Id PutSynonymRule
// @Summary Create or update synonym rule for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Accept  json
// @Produce json
// @Param   id   path string           true "Synonyms set id"
// @Param   rule path string           true "Synonym rule id"
// @Param   data body meta.SynonymRule true "Synonym rule"
// @Success 200 {object} synonyms.UpdateResponse
// @Success 201 {object} synonyms.UpdateResponse
// @Failure 400 {object} map[string]interface{}
// @Router /es/_synonyms/{id}/{rule} [put]
func PutRule(c zhttp.Context) {
	var rule meta.SynonymRule
	if err := zutils.BindJSON(c, &rule); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}
	rule.ID = c.Param("rule_id")
	if err := zedtoken.ValidateSynonymRule(rule.Synonyms); err != nil {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, err.Error())
		return
	}

	id := c.Param("set_id")
	lock.Lock()
	defer lock.Unlock()
	set, ok := loadSet(c, id)
	if !ok {
		return
	}
	if set == nil {
		set = &meta.SynonymSet{ID: id}
	}
	i := ruleIndex(set, rule.ID)
	if i < 0 {
		set.Rules = append(set.Rules, rule)
	} else {
		set.Rules[i] = rule
	}
	saveSet(c, set, i < 0)
}

// DeleteRule deletes a synonym rule and reloads the analyzers using its set
//
// @Id DeleteSynonymRule
// @Summary Delete synonym rule for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Produce json
// @Param   id   path string true "Synonyms set id"
// @Param   rule path string true "Synonym rule id"
// @Success 200 {object} synonyms.UpdateResponse
// @Failure 404 {object} map[string]interface{}
// @Router /es/_synonyms/{id}/{rule} [delete]
func DeleteRule(c zhttp.Context) {
	lock.Lock()
	defer lock.Unlock()
	set, i, ok := loadRule(c)
	if !ok {
		return
	}
	set.Rules = slices.Delete(set.Rules, i, i+1)
	storeSet(c, set, http.StatusOK, "deleted")
}

// ReloadSearchAnalyzers rebuilds the analyzers of an index, picking up
// changed synonyms sets and files
//
// @Id ReloadSearchAnalyzers
// @Summary Reload search analyzers for compatible ES
// @security BasicAuth
// @Tags    Synonyms
// @Produce json
// @Param   index path string true "Index"
// @Success 200 {object} synonyms.ReloadDetails
// @Failure 404 {object} map[string]interface{}
// @Router /es/{index}/_reload_search_analyzers [post]
func ReloadSearchAnalyzers(c zhttp.Context) {
	name := c.Param("target")
	lock.Lock()
	defer lock.Unlock()
	index, ok := core.GetIndex(name)
	if !ok {
		renderError(c, http.StatusNotFound, "index_not_found_exception", fmt.Sprintf("no such index [%s]", name))
		return
	}
	var analyzers []string
	if settings := index.GetSettings(); settings != nil && settings.Analysis != nil {
		for name := range settings.Analysis.Analyzer {
			analyzers = append(analyzers, name)
		}
	}
	sort.Strings(analyzers)

	details := ReloadDetails{ReloadDetails: []ReloadDetail{}}
	reload(&details, index, analyzers)
	zutils.RenderJSON(c, http.StatusOK, details)
}

// prepareRules generates the missing rule ids and validates every rule.
func prepareRules(rules []meta.SynonymRule) error {
	seen := make(map[string]bool, len(rules))
	for i := range rules {
		if rules[i].ID == "" {
			rules[i].ID = ider.Generate()
		}
		if seen[rules[i].ID] {
			return fmt.Errorf("duplicate synonym rule id [%s]", rules[i].ID)
		}
		seen[rules[i].ID] = true
		if err := zedtoken.ValidateSynonymRule(rules[i].Synonyms); err != nil {
			return err
		}
	}
	return nil
}

// saveSet stores set as created or updated, see storeSet.
func saveSet(c zhttp.Context, set *meta.SynonymSet, created bool) {
	if created {
		storeSet(c, set, http.StatusCreated, "created")
		return
	}
	storeSet(c, set, http.StatusOK, "updated")
}

// storeSet builds the analyzers of the indexes using set with its new rules
// and, only when they all build, stores set, installs them and renders the
// result, so invalid rules never reach the storage.
func storeSet(c zhttp.Context, set *meta.SynonymSet, code int, result string) {
	rebuilds, err := buildSet(set)
	if err != nil {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, err.Error())
		return
	}
	if err := metadata.Synonyms.Set(set.ID, *set); err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	details := ReloadDetails{ReloadDetails: []ReloadDetail{}}
	for _, r := range rebuilds {
		install(&details, r.index, r.analyzers, r.built, nil)
	}
	zutils.RenderJSON(c, code, UpdateResponse{Result: result, ReloadAnalyzersDetails: details})
}

// loadSet returns the set id, nil when missing; it renders storage errors.
func loadSet(c zhttp.Context, id string) (*meta.SynonymSet, bool) {
	set, err := metadata.Synonyms.Get(id)
	if errors.Is(err, errors.ErrKeyNotFound) {
		return nil, true
	}
	if err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return nil, false
	}
	return set, true
}

// loadRule returns the set and rule index of the request, rendering 404
// when either is missing.
func loadRule(c zhttp.Context) (*meta.SynonymSet, int, bool) {
	id, ruleID := c.Param("set_id"), c.Param("rule_id")
	set, ok := loadSet(c, id)
	if !ok {
		return nil, 0, false
	}
	if set == nil {
		renderSetNotFound(c, id)
		return nil, 0, false
	}
	i := ruleIndex(set, ruleID)
	if i < 0 {
		renderError(c, http.StatusNotFound, errorTypeNotFound, fmt.Sprintf("synonym rule [%s] not found", ruleID))
		return nil, 0, false
	}
	return set, i, true
}

func ruleIndex(set *meta.SynonymSet, ruleID string) int {
	return slices.IndexFunc(set.Rules, func(r meta.SynonymRule) bool { return r.ID == ruleID })
}

// rebuild is the analyzers of an index built with candidate synonyms rules.
type rebuild struct {
	index     *core.Index
	analyzers []string
	built     map[string]*analysis.Analyzer
}

// buildSet builds the analyzers of every index using set with its rules in
// place of the stored ones.
func buildSet(set *meta.SynonymSet) ([]rebuild, error) {
	rules := make([]string, len(set.Rules))
	for i, rule := range set.Rules {
		rules[i] = rule.Synonyms
	}
	var rebuilds []rebuild
	for _, index := range sortedIndexes() {
		settings := analysisOf(index)
		if settings == nil {
			continue
		}
		// RequestAnalyzer builds every filter, so an unreferenced filter
		// using the set is validated too
		filters := setFilters(settings, set.ID)
		if len(filters) == 0 {
			continue
		}
		built, err := zedanalysis.RequestAnalyzer(withRules(settings, set.ID, rules))
		if err != nil {
			return nil, fmt.Errorf("synonyms set [%s] breaks the analyzers of index [%s]: %s", set.ID, index.GetName(), err)
		}
		if analyzers := analyzersUsing(settings, filters); len(analyzers) > 0 {
			rebuilds = append(rebuilds, rebuild{index: index, analyzers: analyzers, built: built})
		}
	}
	return rebuilds, nil
}

// withRules returns a copy of settings whose token filters using the set id
// get rules inline instead; RequestAnalyzer mutates its input, so the maps
// and analyzers are copied.
func withRules(settings *meta.IndexAnalysis, id string, rules []string) *meta.IndexAnalysis {
	clone := *settings
	clone.Analyzer = make(map[string]*meta.Analyzer, len(settings.Analyzer))
	for name, analyzer := range settings.Analyzer {
		a := *analyzer
		clone.Analyzer[name] = &a
	}
	clone.TokenFilter = filtersWithRules(settings.TokenFilter, id, rules)
	clone.Filter = filtersWithRules(settings.Filter, id, rules)
	return &clone
}

func filtersWithRules(filters map[string]interface{}, id string, rules []string) map[string]interface{} {
	if filters == nil {
		return nil
	}
	out := make(map[string]interface{}, len(filters))
	for name, options := range filters {
		if v, err := zutils.GetStringFromMap(options, "synonyms_set"); err == nil && v == id {
			opts := maps.Clone(options.(map[string]interface{}))
			delete(opts, "synonyms_set")
			inline, err := zutils.GetStringSliceFromMap(opts, "synonyms")
			if err != nil {
				inline = nil
			}
			opts["synonyms"] = slices.Concat(inline, rules)
			options = opts
		}
		out[name] = options
	}
	return out
}

// reload rebuilds the analyzers of index, reporting the given names.
func reload(details *ReloadDetails, index *core.Index, analyzers []string) {
	var settings *meta.IndexAnalysis
	if s := index.GetSettings(); s != nil {
		settings = s.Analysis
	}
	built, err := zedanalysis.RequestAnalyzer(settings)
	install(details, index, analyzers, built, err)
}

// install sets the built analyzers of index unless err, reporting the
// given names.
func install(details *ReloadDetails, index *core.Index, analyzers []string, built map[string]*analysis.Analyzer, err error) {
	details.Shards.Total++
	if err == nil {
		err = index.SetAnalyzers(built)
	}
	if err != nil {
		log.Error().Err(err).Str("index", index.GetName()).Msg("synonyms: reload analyzers")
		details.Shards.Failed++
		return
	}
	details.Shards.Successful++
	if analyzers == nil {
		analyzers = []string{}
	}
	details.ReloadDetails = append(details.ReloadDetails, ReloadDetail{
		Index:             index.GetName(),
		ReloadedAnalyzers: analyzers,
		ReloadedNodeIDs:   []string{},
	})
}

func sortedIndexes() []*core.Index {
	indexes := core.ZED_INDEX_LIST.List()
	sort.Slice(indexes, func(i, j int) bool { return indexes[i].GetName() < indexes[j].GetName() })
	return indexes
}

func analysisOf(index *core.Index) *meta.IndexAnalysis {
	if settings := index.GetSettings(); settings != nil {
		return settings.Analysis
	}
	return nil
}

// setFilters returns the names of the token filters using the set id.
func setFilters(analysis *meta.IndexAnalysis, id string) map[string]bool {
	names := make(map[string]bool)
	for _, filters := range []map[string]interface{}{analysis.TokenFilter, analysis.Filter} {
		for name, options := range filters {
			if v, err := zutils.GetStringFromMap(options, "synonyms_set"); err == nil && v == id {
				names[name] = true
			}
		}
	}
	return names
}

// analyzersUsing returns the sorted names of the analyzers using any of filters.
func analyzersUsing(analysis *meta.IndexAnalysis, filters map[string]bool) []string {
	var names []string
	for name, analyzer := range analysis.Analyzer {
		if slices.ContainsFunc(slices.Concat(analyzer.TokenFilter, analyzer.Filter), func(f string) bool { return filters[f] }) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// page parses the from and size query parameters, rendering 400 when invalid.
func page(c zhttp.Context) (int, int, bool) {
	from, err := strconv.Atoi(c.DefaultQuery("from", "0"))
	if err != nil || from < 0 {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, "[from] must be a non-negative integer")
		return 0, 0, false
	}
	size, err := strconv.Atoi(c.DefaultQuery("size", "10"))
	if err != nil || size < 0 {
		renderError(c, http.StatusBadRequest, errorTypeIllegal, "[size] must be a non-negative integer")
		return 0, 0, false
	}
	return from, size, true
}

func paginate[T any](items []T, from, size int) []T {
	if from >= len(items) {
		return nil
	}
	return items[from : from+min(size, len(items)-from)]
}

func renderSetNotFound(c zhttp.Context, id string) {
	renderError(c, http.StatusNotFound, errorTypeNotFound, fmt.Sprintf("synonyms set [%s] not found", id))
}

func renderError(c zhttp.Context, code int, typ, reason string) {
	zutils.RenderJSON(c, code, zhttp.H{
		"error":  zhttp.H{"type": typ, "reason": reason},
		"status": code,
	})
}
