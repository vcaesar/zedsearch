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
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

// Package ltr evaluates learning to rank models at query time, in pure Go.
// A model has query feature extractors, as Elasticsearch learning_to_rank
// models do, and one of the definitions: an Elasticsearch trained_model tree
// or ensemble, an XGBoost JSON dump or a linear model, as the OpenSearch LTR
// plugin takes them.
package ltr

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// Feature is a query whose score for a document is one input of the model.
type Feature struct {
	Name    string
	Query   map[string]interface{}
	Default float64 // value of a document the query does not match, NaN when missing
}

// Model is a compiled learning to rank model.
type Model struct {
	Features      []Feature
	defaultParams map[string]interface{}
	scorer        scorer
}

type scorer interface {
	score(x []float64) float64
}

func parseErr(format string, a ...interface{}) error {
	return errors.New(errors.ErrorTypeParsingException, "[learning_to_rank] "+fmt.Sprintf(format, a...))
}

// Compile validates m and builds its features and scorer.
func Compile(m *meta.LTRModel) (*Model, error) {
	cfg, err := object(m.InferenceConfig["learning_to_rank"], "inference_config.learning_to_rank")
	if err != nil {
		return nil, err
	}
	model := new(Model)
	if v, ok := cfg["default_params"]; ok {
		if model.defaultParams, err = object(v, "default_params"); err != nil {
			return nil, err
		}
	}
	extractors, err := list(cfg["feature_extractors"], "feature_extractors")
	if err != nil {
		return nil, err
	}
	if len(extractors) == 0 {
		return nil, parseErr("[feature_extractors] must not be empty")
	}
	index := make(map[string]int, len(extractors))
	for _, e := range extractors {
		f, err := parseFeature(e)
		if err != nil {
			return nil, err
		}
		if _, dup := index[f.Name]; dup {
			return nil, parseErr("duplicate feature [%s]", f.Name)
		}
		index[f.Name] = len(model.Features)
		model.Features = append(model.Features, f)
	}
	if len(m.Definition) != 1 {
		return nil, parseErr("[definition] must have exactly one of [trained_model], [xgboost], [linear]")
	}
	names := make([]string, len(model.Features))
	for i, f := range model.Features {
		names[i] = f.Name
	}
	for k, v := range m.Definition {
		switch k {
		case "trained_model":
			model.scorer, err = parseTrainedModel(v, names, index)
		case "xgboost":
			model.scorer, err = parseXGBoost(v, index)
		case "linear":
			model.scorer, err = parseLinear(v, index)
		default:
			err = parseErr("unknown definition [%s]", k)
		}
	}
	if err != nil {
		return nil, err
	}
	return model, nil
}

func parseFeature(v interface{}) (Feature, error) {
	e, err := object(v, "feature_extractors")
	if err != nil {
		return Feature{}, err
	}
	q, err := object(e["query_extractor"], "query_extractor")
	if err != nil {
		return Feature{}, err
	}
	f := Feature{Default: math.NaN()}
	if f.Name, _ = q["feature_name"].(string); f.Name == "" {
		return Feature{}, parseErr("[query_extractor] requires [feature_name]")
	}
	if f.Query, err = object(q["query"], "query_extractor.query"); err != nil {
		return Feature{}, err
	}
	if d, ok := q["default_score"]; ok {
		if f.Default, err = zutils.ToFloat64(d); err != nil {
			return Feature{}, parseErr("[default_score] of [%s] must be a number", f.Name)
		}
	}
	return f, nil
}

// Score returns the model score of feature values x, NaN for a missing value.
func (m *Model) Score(x []float64) float64 {
	return m.scorer.score(x)
}

// Queries returns the feature queries with their {{param}} placeholders
// replaced by params, falling back to the default_params of the model.
func (m *Model) Queries(params map[string]interface{}) ([]map[string]interface{}, error) {
	all := make(map[string]interface{}, len(m.defaultParams)+len(params))
	for k, v := range m.defaultParams {
		all[k] = v
	}
	for k, v := range params {
		all[k] = v
	}
	queries := make([]map[string]interface{}, len(m.Features))
	for i, f := range m.Features {
		q, err := render(f.Query, all)
		if err != nil {
			return nil, err
		}
		queries[i] = q.(map[string]interface{})
	}
	return queries, nil
}

var placeholder = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// render copies v replacing {{name}} in its strings and keys by params. A
// string that is one placeholder takes the param value itself, e.g. a number.
func render(v interface{}, params map[string]interface{}) (interface{}, error) {
	switch v := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, item := range v {
			key, err := renderString(k, params)
			if err != nil {
				return nil, err
			}
			if _, dup := out[key]; dup {
				return nil, parseErr("duplicate key [%s] after rendering params", key)
			}
			if out[key], err = render(item, params); err != nil {
				return nil, err
			}
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			var err error
			if out[i], err = render(item, params); err != nil {
				return nil, err
			}
		}
		return out, nil
	case string:
		if m := placeholder.FindStringSubmatch(v); m != nil && m[0] == v {
			p, ok := params[m[1]]
			if !ok {
				return nil, parseErr("missing param [%s]", m[1])
			}
			return p, nil
		}
		return renderString(v, params)
	}
	return v, nil
}

func renderString(s string, params map[string]interface{}) (string, error) {
	var missing string
	out := placeholder.ReplaceAllStringFunc(s, func(match string) string {
		name := placeholder.FindStringSubmatch(match)[1]
		p, ok := params[name]
		if !ok {
			missing = name
			return match
		}
		str, _ := zutils.ToString(p) // ToString formats any value
		return str
	})
	if missing != "" {
		return "", parseErr("missing param [%s]", missing)
	}
	return out, nil
}

// linear scores bias + the sum of weight * value, a missing value counts 0.
type linear struct {
	weights []float64
	bias    float64
}

func (l *linear) score(x []float64) float64 {
	s := l.bias
	for i, w := range l.weights {
		if !math.IsNaN(x[i]) {
			s += w * x[i]
		}
	}
	return s
}

func parseLinear(v interface{}, index map[string]int) (scorer, error) {
	def, err := object(v, "linear")
	if err != nil {
		return nil, err
	}
	_, bias := def["bias"]
	if _, intercept := def["intercept"]; bias && intercept {
		return nil, parseErr("[linear] cannot have both [bias] and [intercept]")
	}
	l := &linear{weights: make([]float64, len(index))}
	for k, item := range def {
		switch k {
		case "bias", "intercept":
			if l.bias, err = zutils.ToFloat64(item); err != nil {
				return nil, parseErr("[linear] %s must be a number", k)
			}
		case "weights":
			if err = parseWeights(l.weights, item, index); err != nil {
				return nil, err
			}
		default:
			return nil, parseErr("[linear] unknown field [%s]", k)
		}
	}
	return l, nil
}

// parseWeights reads weights by feature name or as a list in feature order.
func parseWeights(dst []float64, v interface{}, index map[string]int) error {
	switch v := v.(type) {
	case map[string]interface{}:
		for name, w := range v {
			i, ok := index[name]
			if !ok {
				return parseErr("[linear] unknown feature [%s]", name)
			}
			f, err := zutils.ToFloat64(w)
			if err != nil {
				return parseErr("[linear] weight of [%s] must be a number", name)
			}
			dst[i] = f
		}
		return nil
	case []interface{}:
		if len(v) != len(dst) {
			return parseErr("[linear] has %d weights for %d features", len(v), len(dst))
		}
		for i, w := range v {
			f, err := zutils.ToFloat64(w)
			if err != nil {
				return parseErr("[linear] weight %d must be a number", i)
			}
			dst[i] = f
		}
		return nil
	}
	return parseErr("[linear] weights must be an object or an array")
}

// ensemble sums the weighted scores of its models, through a sigmoid for
// logistic_regression.
type ensemble struct {
	models   []scorer
	weights  []float64
	base     float64
	logistic bool
}

func (e *ensemble) score(x []float64) float64 {
	s := e.base
	for i, m := range e.models {
		s += e.weights[i] * m.score(x)
	}
	if e.logistic {
		return 1 / (1 + math.Exp(-s))
	}
	return s
}

// parseTrainedModel parses an Elasticsearch trained_model: a tree or an
// ensemble of trained models. names are the features split_feature indexes.
func parseTrainedModel(v interface{}, names []string, index map[string]int) (scorer, error) {
	def, err := object(v, "trained_model")
	if err != nil {
		return nil, err
	}
	if len(def) != 1 {
		return nil, parseErr("[trained_model] must have exactly one of [tree], [ensemble]")
	}
	if t, ok := def["tree"]; ok {
		return parseESTree(t, names, index)
	}
	body, ok := def["ensemble"]
	if !ok {
		return nil, parseErr("[trained_model] must have exactly one of [tree], [ensemble]")
	}
	e, err := object(body, "ensemble")
	if err != nil {
		return nil, err
	}
	if names, err = featureNames(e, names); err != nil {
		return nil, err
	}
	models, err := list(e["trained_models"], "ensemble.trained_models")
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, parseErr("[ensemble] trained_models must not be empty")
	}
	ens := &ensemble{weights: make([]float64, len(models))}
	for i, m := range models {
		s, err := parseTrainedModel(m, names, index)
		if err != nil {
			return nil, err
		}
		ens.models = append(ens.models, s)
		ens.weights[i] = 1
	}
	if agg, ok := e["aggregate_output"]; ok {
		if err := parseAggregate(ens, agg); err != nil {
			return nil, err
		}
	}
	return ens, nil
}

func parseAggregate(ens *ensemble, v interface{}) error {
	agg, err := object(v, "aggregate_output")
	if err != nil {
		return err
	}
	if len(agg) != 1 {
		return parseErr("[aggregate_output] must have exactly one of [weighted_sum], [logistic_regression]")
	}
	for k, item := range agg {
		switch k {
		case "weighted_sum":
		case "logistic_regression":
			ens.logistic = true
		default:
			return parseErr("unsupported aggregate_output [%s]", k)
		}
		body, err := object(item, k)
		if err != nil {
			return err
		}
		w, ok := body["weights"]
		if !ok {
			return nil
		}
		weights, err := list(w, k+".weights")
		if err != nil {
			return err
		}
		if len(weights) != len(ens.models) {
			return parseErr("[%s] has %d weights for %d models", k, len(weights), len(ens.models))
		}
		for i, item := range weights {
			if ens.weights[i], err = zutils.ToFloat64(item); err != nil {
				return parseErr("[%s] weight %d must be a number", k, i)
			}
		}
	}
	return nil
}

// featureNames returns the feature_names of def, names when it has none.
func featureNames(def map[string]interface{}, names []string) ([]string, error) {
	v, ok := def["feature_names"]
	if !ok {
		return names, nil
	}
	items, err := list(v, "feature_names")
	if err != nil {
		return nil, err
	}
	out := make([]string, len(items))
	for i, item := range items {
		if out[i], ok = item.(string); !ok {
			return nil, parseErr("[feature_names] must be strings")
		}
	}
	return out, nil
}

// node is a split, or a leaf when feature is -1. left and right are
// positions in tree.nodes.
type node struct {
	feature     int
	threshold   float64
	op          string // lt, lte, gt, gte: go left when value op threshold
	left, right int
	defaultLeft bool // side of a missing value
	value       float64
}

type tree struct {
	nodes []node // nodes[0] is the root
}

func (t *tree) score(x []float64) float64 {
	n := &t.nodes[0]
	for n.feature >= 0 {
		v := x[n.feature]
		left := n.defaultLeft
		if !math.IsNaN(v) {
			switch n.op {
			case "lt":
				left = v < n.threshold
			case "gt":
				left = v > n.threshold
			case "gte":
				left = v >= n.threshold
			default:
				left = v <= n.threshold
			}
		}
		if left {
			n = &t.nodes[n.left]
		} else {
			n = &t.nodes[n.right]
		}
	}
	return n.value
}

// rawNode is a node referring to its children by node id.
type rawNode struct {
	node
	id, leftID, rightID int
}

// buildTree orders raw from the root, checking every split has both children
// and every node is reached once, so that scoring always ends at a leaf.
func buildTree(raw []rawNode, rootID int) (*tree, error) {
	byID := make(map[int]int, len(raw))
	for i, r := range raw {
		if _, dup := byID[r.id]; dup {
			return nil, parseErr("duplicate node [%d]", r.id)
		}
		byID[r.id] = i
	}
	t := &tree{nodes: make([]node, 0, len(raw))}
	pos := make(map[int]int, len(raw))
	var order []int // raw position of each node of t
	stack := []int{rootID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		i, ok := byID[id]
		if !ok {
			return nil, parseErr("missing node [%d]", id)
		}
		if _, seen := pos[id]; seen {
			return nil, parseErr("node [%d] is reached more than once", id)
		}
		pos[id] = len(t.nodes)
		t.nodes = append(t.nodes, raw[i].node)
		order = append(order, i)
		if raw[i].feature >= 0 {
			stack = append(stack, raw[i].rightID, raw[i].leftID)
		}
	}
	for i := range t.nodes {
		if t.nodes[i].feature >= 0 {
			r := raw[order[i]]
			t.nodes[i].left, t.nodes[i].right = pos[r.leftID], pos[r.rightID]
		}
	}
	if len(t.nodes) != len(raw) {
		return nil, parseErr("%d nodes are not reachable from the root", len(raw)-len(t.nodes))
	}
	return t, nil
}

// parseESTree parses an Elasticsearch tree: tree_structure nodes with
// node_index, split_feature, threshold, decision_type, default_left,
// left_child, right_child and leaf_value.
func parseESTree(v interface{}, names []string, index map[string]int) (scorer, error) {
	def, err := object(v, "tree")
	if err != nil {
		return nil, err
	}
	if names, err = featureNames(def, names); err != nil {
		return nil, err
	}
	items, err := list(def["tree_structure"], "tree.tree_structure")
	if err != nil {
		return nil, err
	}
	raw := make([]rawNode, len(items))
	for i, item := range items {
		n, err := object(item, "tree_structure")
		if err != nil {
			return nil, err
		}
		if raw[i], err = parseESNode(n, names, index); err != nil {
			return nil, err
		}
	}
	return buildTree(raw, 0)
}

func parseESNode(n map[string]interface{}, names []string, index map[string]int) (rawNode, error) {
	r := rawNode{node: node{feature: -1, op: "lte", defaultLeft: true}}
	var err error
	if r.id, err = intField(n, "node_index"); err != nil {
		return r, err
	}
	if lv, ok := n["leaf_value"]; ok {
		if arr, isList := lv.([]interface{}); isList && len(arr) > 0 {
			lv = arr[0]
		}
		if r.value, err = zutils.ToFloat64(lv); err != nil {
			return r, parseErr("[leaf_value] of node [%d] must be a number", r.id)
		}
		return r, nil
	}
	split, err := intField(n, "split_feature")
	if err != nil {
		return r, err
	}
	if split < 0 || split >= len(names) {
		return r, parseErr("[split_feature] %d of node [%d] is out of range", split, r.id)
	}
	var ok bool
	if r.feature, ok = index[names[split]]; !ok {
		return r, parseErr("unknown feature [%s]", names[split])
	}
	if r.threshold, err = floatField(n, "threshold"); err != nil {
		return r, err
	}
	if v, has := n["decision_type"]; has {
		dt, _ := v.(string)
		switch strings.ToLower(dt) {
		case "lt", "lte", "gt", "gte":
			r.op = strings.ToLower(dt)
		default:
			return r, parseErr("unknown decision_type [%v]", v)
		}
	}
	if v, has := n["default_left"]; has {
		if r.defaultLeft, ok = v.(bool); !ok {
			return r, parseErr("[default_left] of node [%d] must be a boolean", r.id)
		}
	}
	if r.leftID, err = intField(n, "left_child"); err != nil {
		return r, err
	}
	r.rightID, err = intField(n, "right_child")
	return r, err
}

var xgbFeature = regexp.MustCompile(`^f(\d+)$`)

// xgbFeatureIndex returns the model feature named name; f<N> names the Nth
// feature as XGBoost does without feature names.
func xgbFeatureIndex(name string, index map[string]int) (int, error) {
	if i, ok := index[name]; ok {
		return i, nil
	}
	if m := xgbFeature.FindStringSubmatch(name); m != nil {
		if i, err := strconv.Atoi(m[1]); err == nil && i < len(index) {
			return i, nil
		}
	}
	return 0, parseErr("unknown feature [%s]", name)
}

// parseXGBoost parses an XGBoost JSON dump: the list of tree objects, or
// {"objective", "base_score", "splits"} with it as splits. Each string
// booster.get_dump(dump_format="json") returns is one tree, to decode into
// its object before sending the list. A logistic base_score is a probability,
// it is added as its logit.
func parseXGBoost(v interface{}, index map[string]int) (scorer, error) {
	ens := &ensemble{}
	splits := v
	if def, ok := v.(map[string]interface{}); ok {
		for k, item := range def {
			var err error
			switch k {
			case "splits":
			case "base_score":
				if ens.base, err = zutils.ToFloat64(item); err != nil {
					return nil, parseErr("[xgboost] base_score must be a number")
				}
			case "objective":
				switch item {
				case "reg:linear", "reg:squarederror", "rank:pairwise", "rank:ndcg", "rank:map":
				case "reg:logistic", "binary:logistic":
					ens.logistic = true
				default:
					return nil, parseErr("[xgboost] unsupported objective [%v]", item)
				}
			default:
				return nil, parseErr("[xgboost] unknown field [%s]", k)
			}
		}
		splits = def["splits"]
		if _, ok := def["base_score"]; ok && ens.logistic {
			if ens.base <= 0 || ens.base >= 1 {
				return nil, parseErr("[xgboost] base_score of a logistic objective must be in (0, 1)")
			}
			ens.base = math.Log(ens.base / (1 - ens.base))
		}
	}
	trees, err := list(splits, "xgboost")
	if err != nil {
		return nil, err
	}
	if len(trees) == 0 {
		return nil, parseErr("[xgboost] must have trees")
	}
	for _, t := range trees {
		root, err := object(t, "xgboost tree")
		if err != nil {
			return nil, err
		}
		var raw []rawNode
		if err := flattenXGBoost(root, index, &raw); err != nil {
			return nil, err
		}
		built, err := buildTree(raw, raw[0].id)
		if err != nil {
			return nil, err
		}
		ens.models = append(ens.models, built)
		ens.weights = append(ens.weights, 1)
	}
	return ens, nil
}

func flattenXGBoost(n map[string]interface{}, index map[string]int, raw *[]rawNode) error {
	r := rawNode{node: node{feature: -1, op: "lt", defaultLeft: true}}
	var err error
	if r.id, err = intField(n, "nodeid"); err != nil {
		return err
	}
	if lv, ok := n["leaf"]; ok {
		if r.value, err = zutils.ToFloat64(lv); err != nil {
			return parseErr("[leaf] of node [%d] must be a number", r.id)
		}
		*raw = append(*raw, r)
		return nil
	}
	name, _ := n["split"].(string)
	if r.feature, err = xgbFeatureIndex(name, index); err != nil {
		return err
	}
	if r.threshold, err = floatField(n, "split_condition"); err != nil {
		return err
	}
	if r.leftID, err = intField(n, "yes"); err != nil {
		return err
	}
	if r.rightID, err = intField(n, "no"); err != nil {
		return err
	}
	if _, ok := n["missing"]; ok {
		missing, err := intField(n, "missing")
		if err != nil {
			return err
		}
		if missing != r.leftID && missing != r.rightID {
			return parseErr("[missing] %d of node [%d] must be its yes or no child", missing, r.id)
		}
		r.defaultLeft = missing == r.leftID
	}
	*raw = append(*raw, r)
	children, err := list(n["children"], "children")
	if err != nil {
		return err
	}
	for _, c := range children {
		child, err := object(c, "children")
		if err != nil {
			return err
		}
		if err := flattenXGBoost(child, index, raw); err != nil {
			return err
		}
	}
	return nil
}

func object(v interface{}, what string) (map[string]interface{}, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, parseErr("[%s] must be an object", what)
	}
	return m, nil
}

func list(v interface{}, what string) ([]interface{}, error) {
	l, ok := v.([]interface{})
	if !ok {
		return nil, parseErr("[%s] must be an array", what)
	}
	return l, nil
}

func intField(m map[string]interface{}, key string) (int, error) {
	v, ok := m[key]
	if !ok {
		return 0, parseErr("node requires [%s]", key)
	}
	f, err := zutils.ToFloat64(v)
	// past 2^53 distinct ids decode to the same float64
	if err != nil || f != math.Trunc(f) || math.Abs(f) >= 1<<53 {
		return 0, parseErr("[%s] must be an integer", key)
	}
	return int(f), nil
}

func floatField(m map[string]interface{}, key string) (float64, error) {
	v, ok := m[key]
	if !ok {
		return 0, parseErr("node requires [%s]", key)
	}
	f, err := zutils.ToFloat64(v)
	if err != nil {
		return 0, parseErr("[%s] must be a number", key)
	}
	return f, nil
}
