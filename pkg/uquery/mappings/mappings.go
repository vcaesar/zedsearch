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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
or production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package mappings

import (
	"fmt"
	"strings"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	zedanalysis "github.com/vcaesar/zedsearch/pkg/uquery/analysis"
	"github.com/vcaesar/zedsearch/pkg/zutils"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func Request(analyzers map[string]*analysis.Analyzer, data map[string]interface{}) (*meta.Mappings, error) {
	if len(data) == 0 {
		return nil, nil
	}

	if sql, ok := data["sql"]; ok {
		if !config.Global.EnableSQLMappings {
			return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] SQL mappings are disabled")
		}
		if len(data) != 1 {
			return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] sql cannot be combined with other mapping options")
		}
		ddl, ok := sql.(string)
		if !ok || strings.TrimSpace(ddl) == "" {
			return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] sql should be a non-empty string")
		}
		properties, err := sqlProperties(ddl)
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] "+err.Error())
		}
		return Request(analyzers, map[string]interface{}{"properties": properties})
	}

	source, err := sourceMapping("_source", data["_source"])
	if err != nil {
		return nil, err
	}
	all, err := sourceMapping("_all", data["_all"])
	if err != nil {
		return nil, err
	}
	if data["properties"] == nil && (source != nil || all != nil) && len(data) == btoi(source != nil)+btoi(all != nil) {
		data = map[string]interface{}{"properties": map[string]interface{}{}}
	}
	if data["properties"] == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] properties should be defined")
	}

	properties, ok := data["properties"].(map[string]interface{})
	if !ok {
		return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] properties should be an object")
	}

	mappings := meta.NewMappings()
	mappings.Source = source
	mappings.All = all
	for field, prop := range properties {
		var propFields map[string]interface{}

		prop, ok := prop.(map[string]interface{})
		if !ok {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] should be an object", field))
		}

		if v, ok := prop["properties"]; ok {
			if _, ok := v.(map[string]interface{}); !ok {
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] should be an object", field))
			}

			subMappings, err := Request(analyzers, prop)
			if err != nil {
				return nil, err
			}
			subProperties := subMappings.ListProperty()
			for k, v := range subProperties {
				mappings.SetProperty(field+"."+k, v)
			}

			// nested property keeps its sub-fields, every element of nested
			// array is indexed as own hidden document.
			if propType, ok := prop["type"].(string); ok && strings.ToLower(propType) == "nested" {
				nestedProp := newNestedProperty()
				nestedProp.Properties = subProperties
				mappings.SetProperty(field, nestedProp)
				setNestedFields(mappings)
			}

			continue
		}

		if v, ok := prop["fields"]; ok {
			if propFields, ok = v.(map[string]interface{}); !ok {
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] property.fields [%s] should be an object, got %T", field, v))
			}

			// check if sub field contains property "type"
			for _, subProp := range v.(map[string]any) {
				if subPropField, ok := subProp.(map[string]any); ok {
					subPropType, ok := subPropField["type"]
					if !ok {
						return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] should be exists", "type"))
					}
					if _, ok = subPropType.(string); !ok {
						return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] should be a string", "type"))
					}
				}
			}
		}

		propType, ok := prop["type"]
		if !ok {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] should be exists", "type"))
		}

		propTypeStr, ok := propType.(string)
		if !ok {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] should be a string", "type"))
		}

		var newProp meta.Property
		propTypeStr = strings.ToLower(propTypeStr)
		switch propTypeStr {
		case "text":
			newProp = meta.NewProperty(propTypeStr)

			if config.Global.EnableTextKeywordMapping {
				p := meta.NewProperty("keyword")
				newProp.AddField("keyword", p)
			}
		case "keyword", "numeric", "bool", "date":
			newProp = meta.NewProperty(propTypeStr)
		case "constant_keyword":
			newProp = meta.NewProperty("keyword")
		case "match_only_text":
			newProp = meta.NewProperty("text")
		case "integer", "double", "long", "short", "int", "float":
			newProp = meta.NewProperty("numeric")
		case "boolean":
			newProp = meta.NewProperty("bool")
		case "time", "datetime":
			newProp = meta.NewProperty("date")
		case "geo_point", "geo":
			newProp = meta.NewProperty("geo_point")
		case "vector", "dense_vector", "knn_vector":
			newProp = meta.NewProperty("vector")
		case "nested":
			// nested property without declared sub-fields, sub-fields of
			// nested documents are mapped dynamically.
			newProp = newNestedProperty()
			setNestedFields(mappings)
		case "join":
			newProp = meta.NewProperty("join")
			newProp.Sortable = false
			newProp.Aggregatable = false
			relations, err := parseRelations(field, prop["relations"])
			if err != nil {
				return nil, err
			}
			newProp.Relations = relations
			setJoinFields(mappings)
		case "flattened", "object", "wildcard", "byte", "alias", "ip", "ip_range", "scaled_float":
			// ignore
		default:
			return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[mappings] properties [%s] doesn't support type [%s]", field, propTypeStr))
		}

		var indexOptionsKey string
		var compression, mode interface{}
		for k, v := range prop {
			switch k {
			case "type":
				// handled
			case "analyzer":
				newProp.Analyzer = v.(string)
			case "search_analyzer":
				newProp.SearchAnalyzer = v.(string)
			case "format":
				newProp.Format = v.(string)
			case "time_zone":
				newProp.TimeZone = v.(string)
				_, err := zutils.ParseTimeZone(newProp.TimeZone)
				if err != nil {
					return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] %s time_zone parse err %s", field, err.Error()))
				}
			case "index":
				newProp.Index = v.(bool)
			case "dims", "dimension":
				dims, err := zutils.ToInt(v)
				if err != nil || dims <= 0 {
					return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] dims must be a positive integer", field))
				}
				newProp.Dims = dims
			case "index_options", "method":
				opts, err := parseVectorIndexOptions(field, k, v)
				if err != nil {
					return nil, err
				}
				newProp.IndexOptions, indexOptionsKey = opts, k
				if m, ok := v.(map[string]interface{}); ok && k == "method" && m["space_type"] != nil {
					if newProp.Similarity, err = parseVectorSimilarity(field, m["space_type"]); err != nil {
						return nil, err
					}
				}
			case "similarity", "space_type":
				// on other types similarity names a text scoring model, ignored
				if newProp.Type != "vector" {
					continue
				}
				metric, err := parseVectorSimilarity(field, v)
				if err != nil {
					return nil, err
				}
				newProp.Similarity = metric
			case "compression_level":
				compression = v
			case "mode":
				mode = v
			case "store":
				newProp.Store = v.(bool)
			case "sortable":
				newProp.Sortable = v.(bool)
			case "aggregatable":
				newProp.Aggregatable = v.(bool)
			case "highlightable":
				newProp.Highlightable = v.(bool)
			default:
				// ignore unknown options
				// return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] unknown option [%s]", field, k))
			}
		}

		if newProp.Highlightable {
			newProp.Store = true
		}

		if newProp.Type == "vector" && (compression != nil || mode != nil) {
			if err := applyVectorCompression(field, &newProp, indexOptionsKey, compression, mode); err != nil {
				return nil, err
			}
		}

		if newProp.Type == "vector" {
			// vector fields are stored-only: no terms, doc values or highlights
			newProp.Sortable = false
			newProp.Aggregatable = false
			newProp.Highlightable = false
		} else if newProp.Dims != 0 || newProp.IndexOptions != nil || newProp.Similarity != "" {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] dims, index_options and space_type are only valid for type [vector]", field))
		}

		if newProp.Type != "" {
			mappings.SetProperty(field, newProp)
		}

		if newProp.Type == "text" {
			fields, err := convertToField(propFields)
			if err != nil {
				return nil, err
			}

			for k, v := range fields {
				newProp.AddField(k, v)
				mappings.SetProperty(field+"."+k, v)
			}

			// check analyzer
			if newProp.Analyzer != "" {
				if _, err := zedanalysis.QueryAnalyzer(analyzers, newProp.Analyzer); err != nil {
					return nil, err
				}
			}
			if newProp.SearchAnalyzer != "" {
				if _, err := zedanalysis.QueryAnalyzer(analyzers, newProp.SearchAnalyzer); err != nil {
					return nil, err
				}
			}
		}
	}

	return mappings, nil
}

// sourceMapping parses the Elasticsearch `_source` or `_all` mapping, only "enabled".
func sourceMapping(name string, v interface{}) (*meta.SourceMapping, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] "+name+" should be an object")
	}
	source := &meta.SourceMapping{Enabled: true}
	for k, v := range m {
		if k != "enabled" {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] %s does not support [%s]", name, k))
		}
		enabled, err := zutils.ToBool(v)
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, "[mappings] "+name+".enabled should be a boolean")
		}
		source.Enabled = enabled
	}
	return source, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// newNestedProperty returns property of "nested" type. Nested property holds
// no value of its own, every element of nested array is indexed as hidden
// document, so it is neither sortable nor aggregatable.
func newNestedProperty() meta.Property {
	prop := meta.NewProperty("nested")
	prop.Sortable = false
	prop.Aggregatable = false
	return prop
}

// setNestedFields registers hidden fields tagging nested documents.
func setNestedFields(mappings *meta.Mappings) {
	for _, field := range []string{meta.NestedPathField, meta.NestedParentField} {
		if _, ok := mappings.GetProperty(field); !ok {
			mappings.SetProperty(field, meta.NewProperty("keyword"))
		}
	}
}

// setJoinFields registers hidden fields tagging join documents.
func setJoinFields(mappings *meta.Mappings) {
	for _, field := range []string{meta.JoinNameField, meta.JoinParentField} {
		if _, ok := mappings.GetProperty(field); !ok {
			mappings.SetProperty(field, meta.NewProperty("keyword"))
		}
	}
}

// parseVectorSimilarity parses the Elasticsearch similarity or OpenSearch
// space_type of a vector field.
func parseVectorSimilarity(field string, v interface{}) (riot.Metric, error) {
	fail := func(msg string) error {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] %s", field, msg))
	}
	name, err := zutils.ToString(v)
	if err != nil {
		return "", fail("similarity must be a string")
	}
	metric, err := meta.ParseVectorMetric(name)
	if err != nil {
		return "", fail(err.Error())
	}
	return metric, nil
}

// vectorIndexLimits bounds HNSW parameters like Elasticsearch does.
var vectorIndexLimits = map[string][2]int{"m": {2, 512}, "ef_construction": {1, 3200}}

// parseVectorIndexOptions parses the HNSW parameters of a vector field from
// Elasticsearch index_options {"type": "int8_hnsw", "m": 16, "ef_construction": 100}
// or OpenSearch method {"name": "hnsw", "parameters": {"m": 16, "ef_construction": 100}}.
// Elasticsearch defaults to int8_hnsw, OpenSearch hnsw to float32 graphs.
// Flat (brute force) types return nil, keeping exact search for the field.
func parseVectorIndexOptions(field, key string, v interface{}) (*meta.VectorIndexOptions, error) {
	fail := func(msg string) error {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] %s %s", field, key, msg))
	}
	raw, ok := v.(map[string]interface{})
	if !ok {
		return nil, fail("must be an object")
	}
	kind, params := "type", raw
	if key == "method" {
		kind, params = "name", nil
		if p, ok := raw["parameters"]; ok {
			if params, ok = p.(map[string]interface{}); !ok {
				return nil, fail("parameters must be an object")
			}
		}
	}
	opts := &meta.VectorIndexOptions{Type: meta.VectorIndexInt8HNSW}
	if key == "method" {
		opts.Type = meta.VectorIndexHNSW
	}
	if t, ok := raw[kind]; ok {
		name, err := zutils.ToString(t)
		if err != nil {
			return nil, fail(kind + " must be a string")
		}
		switch name = strings.ToLower(name); {
		case name == "flat" || name == "int8_flat":
			return nil, nil
		case name == meta.VectorIndexHNSW || (key != "method" && name == meta.VectorIndexInt8HNSW):
			opts.Type = name
		case key == "method":
			return nil, fail(fmt.Sprintf("unsupported %s [%s], expected hnsw", kind, name))
		default:
			return nil, fail(fmt.Sprintf("unsupported %s [%s], expected hnsw, int8_hnsw, flat or int8_flat", kind, name))
		}
	}
	for name, dst := range map[string]*int{"m": &opts.M, "ef_construction": &opts.EfConstruction} {
		pv, ok := params[name]
		if !ok {
			continue
		}
		limit := vectorIndexLimits[name]
		n, err := zutils.ToInt(pv)
		if err != nil || n < limit[0] || n > limit[1] {
			return nil, fail(fmt.Sprintf("%s must be an integer in [%d, %d]", name, limit[0], limit[1]))
		}
		*dst = n
	}
	return opts, nil
}

// vectorCompressionTypes maps OpenSearch compression_level to index types;
// riot graphs hold float32 vectors or int8 codes.
var vectorCompressionTypes = map[string]string{"1x": meta.VectorIndexHNSW, "4x": meta.VectorIndexInt8HNSW}

// applyVectorCompression applies OpenSearch knn_vector compression_level
// and mode to prop. Without compression_level, mode on_disk takes 4x, the
// highest riot supports, and in_memory 1x. Either enables HNSW search.
func applyVectorCompression(field string, prop *meta.Property, indexOptionsKey string, compression, mode interface{}) error {
	fail := func(msg string) error {
		return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] %s", field, msg))
	}
	level := "1x"
	if mode != nil {
		name, err := zutils.ToString(mode)
		switch name = strings.ToLower(name); {
		case err != nil || (name != "in_memory" && name != "on_disk"):
			return fail(fmt.Sprintf("unsupported mode [%v], expected in_memory or on_disk", mode))
		case name == "on_disk":
			level = "4x"
		}
	}
	if compression != nil {
		name, err := zutils.ToString(compression)
		if err != nil {
			return fail("compression_level must be a string")
		}
		level = strings.ToLower(name)
	}
	typ, ok := vectorCompressionTypes[level]
	switch {
	case ok:
	case level == "2x" || level == "8x" || level == "16x" || level == "32x":
		return fail(fmt.Sprintf("compression_level [%s] is not supported, expected 1x or 4x", level))
	default:
		return fail(fmt.Sprintf("invalid compression_level [%s], expected 1x or 4x", level))
	}
	switch {
	case indexOptionsKey != "" && prop.IndexOptions == nil:
		return fail("compression_level and mode require an hnsw index")
	case prop.IndexOptions == nil:
		prop.IndexOptions = &meta.VectorIndexOptions{}
	case indexOptionsKey == "index_options" && prop.IndexOptions.Type != typ:
		return fail(fmt.Sprintf("compression_level [%s] conflicts with index_options type [%s]", level, prop.IndexOptions.Type))
	}
	prop.IndexOptions.Type = typ
	return nil
}

// parseRelations parses relations of "join" property, mapping name of parent
// relation to names of its children: {"question": ["answer", "comment"]}.
func parseRelations(field string, v interface{}) (map[string][]string, error) {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] join requires [relations] object", field))
	}
	if len(raw) == 0 {
		return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] join requires [relations] object", field))
	}

	relations := make(map[string][]string, len(raw))
	for parent, children := range raw {
		if parent == "" {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] relations name should be non-empty", field))
		}
		var names []string
		switch children := children.(type) {
		case string:
			names = []string{children}
		case []interface{}:
			for _, child := range children {
				name, ok := child.(string)
				if !ok {
					return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] relations [%s] should be string or array of strings", field, parent))
				}
				names = append(names, name)
			}
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] relations [%s] should be string or array of strings", field, parent))
		}

		seen := make(map[string]struct{}, len(names))
		for _, name := range names {
			if name == "" || name == parent {
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] relations [%s] has invalid child [%s]", field, parent, name))
			}
			if _, ok := seen[name]; ok {
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[mappings] properties [%s] relations [%s] has duplicated child [%s]", field, parent, name))
			}
			seen[name] = struct{}{}
		}
		relations[parent] = names
	}

	return relations, nil
}

// convertToField converst v to type map[string]meta.Property.
func convertToField(v map[string]interface{}) (map[string]meta.Property, error) {
	r := make(map[string]meta.Property)

	if len(v) == 0 || v == nil {
		return r, nil
	}

	// it seems inefficient to encode and then directly decode it.
	// But in favor of code maintainability and to prevent that we need to maintain
	// duplicated code its more "efficient" to parse the fields this way.

	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}

	return r, nil
}
