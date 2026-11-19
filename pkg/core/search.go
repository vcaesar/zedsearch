/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI.
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
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package core

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/analysis/analyzer"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/highlight"

	zedsearch "github.com/vcaesar/zedsearch/pkg/bluge/search"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery"
	zedanalysis "github.com/vcaesar/zedsearch/pkg/uquery/analysis"
	"github.com/vcaesar/zedsearch/pkg/uquery/fields"
	zedhighlight "github.com/vcaesar/zedsearch/pkg/uquery/highlight"
	uquerydsl "github.com/vcaesar/zedsearch/pkg/uquery/query"
	"github.com/vcaesar/zedsearch/pkg/uquery/source"
	"github.com/vcaesar/zedsearch/pkg/uquery/timerange"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func (index *Index) Search(query *meta.ZedQuery) (*meta.SearchResponse, error) {
	mappings := index.GetMappings()
	analyzers := index.GetAnalyzers()

	timeMin, timeMax := timerange.Query(query.Query)
	readers, err := index.GetReaders(timeMin, timeMax)
	if err != nil {
		log.Printf("index.SearchV2: error accessing reader: %s", err.Error())
		return nil, err
	}
	defer func() {
		for _, reader := range readers {
			reader.Close()
		}
	}()

	// nested and join queries are resolved before query is parsed, and hidden
	// nested documents are excluded from results.
	if err := rewriteJoinQuery(query, mappings, analyzers, readers); err != nil {
		return nil, err
	}

	if _, err = uquery.ParseQueryDSL(query, mappings, analyzers); err != nil {
		return nil, err
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if query.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(query.Timeout)*time.Second)
		defer cancel()
	}

	// dmi, err := riot.MultiSearch(ctx, searchRequest, readers...)
	dmi, err := zedsearch.MultiSearch(ctx, query, mappings, analyzers, readers...)
	if err != nil {
		log.Printf("index.SearchV2: error executing search: %s", err.Error())
		if err == context.DeadlineExceeded {
			return &meta.SearchResponse{
				TimedOut: true,
				Error:    err.Error(),
				Hits:     meta.Hits{Hits: []meta.Hit{}},
			}, nil
		}
		return nil, err
	}

	return searchV2(index.GetAllShardNum(), int64(len(readers)), dmi, query, mappings, analyzers, nil)
}

// searchV2 renders the hits of dmi. Highlighting and fields use the mappings
// and analyzers of the hit's index in indexes, mappings and analyzers otherwise.
func searchV2(shardNum, readerNum int64, dmi search.DocumentMatchIterator, query *meta.ZedQuery,
	mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer, indexes map[string]*Index) (*meta.SearchResponse, error) {
	resp := &meta.SearchResponse{
		Hits: meta.Hits{Hits: []meta.Hit{}},
	}

	// highlight
	var highlighter *highlight.SimpleHighlighter
	if query.Highlight != nil {
		if len(query.Highlight.PreTags) > 0 && len(query.Highlight.PostTags) > 0 {
			highlighter = highlight.NewHTMLHighlighterTags(query.Highlight.PreTags[0], query.Highlight.PostTags[0])
		} else {
			highlighter = highlight.NewHTMLHighlighter()
		}
	}
	highlightOn := query.Highlight != nil && len(query.Highlight.Fields) > 0
	highlightIndexes := make(map[string]*highlightIndex)
	highlightFor := func(indexName string) *highlightIndex {
		if h, ok := highlightIndexes[indexName]; ok {
			return h
		}
		m, a := mappings, analyzers
		if index, ok := indexes[indexName]; ok {
			m, a = index.GetMappings(), index.GetAnalyzers()
		}
		h := &highlightIndex{mappings: m, analyzers: highlightFieldAnalyzers(query.Highlight, m, a)}
		highlightIndexes[indexName] = h
		return h
	}
	// highlightField returns the fragments of value, nil when it has no matched
	// term. number_of_fragments counts over all values of the field, have of
	// them are found already.
	highlightField := func(next *search.DocumentMatch, zer *analysis.Analyzer, field string, value []byte, have int) []string {
		options, ok := query.Highlight.Fields[field]
		if !ok || have >= options.NumberOfFragments {
			return nil
		}
		num := options.NumberOfFragments - have
		v, ok := next.Locations[field]
		if !ok {
			return nil
		}
		v = zedhighlight.Locations(zer, value, v)
		if len(v) == 0 {
			return nil
		}
		if len(options.PreTags) > 0 && len(options.PostTags) > 0 {
			return highlight.NewHTMLHighlighterTags(options.PreTags[0], options.PostTags[0]).BestFragments(v, value, num)
		}
		return highlighter.BestFragments(v, value, num)
	}

	Hits := make([]meta.Hit, 0)
	// declared once and reset per hit: the stored fields closure below makes
	// them escape, so per-hit declarations would cost one allocation each
	var id, indexName string
	var timestamp time.Time
	var sourceData, fieldsData, highlightData map[string]interface{}
	var rawSource, storedSource []byte
	var hasSource bool
	// stored values to highlight, kept until _index, stored after them, is known
	var storedValues []byte
	var storedFields []storedField
	// restoring only applies the fields a document lists as excluded, so the
	// vector fields of all indexes are safe for each hit
	vectorFields := mappings.VectorFields()
	for _, index := range indexes {
		if m := index.GetMappings(); m != mappings {
			vectorFields = append(vectorFields, m.VectorFields()...)
		}
	}
	vectors := meta.NewSourceVectors(vectorFields)
	next, err := dmi.Next()
	for err == nil && next != nil {
		id, indexName, timestamp = "", "", time.Time{}
		sourceData, fieldsData, highlightData = nil, nil, nil
		rawSource, storedSource, hasSource = rawSource[:0], storedSource[:0], false
		storedValues, storedFields = storedValues[:0], storedFields[:0]
		vectors.Reset()
		if query.Highlight != nil {
			highlightData = make(map[string]interface{})
		}
		err = next.VisitStoredFields(func(field string, value []byte) bool {
			switch field {
			case "_id":
				id = string(value)
			case "_index":
				indexName = string(value)
			case "@timestamp":
				timestamp, _ = riot.DecodeDateTime(value)
			case "_source":
				storedSource, hasSource = append(storedSource, value...), true
				if highlightOn {
					rawSource = append(rawSource, value...)
				}
			default:
				// not either or: a field another index maps as a vector may
				// be the stored text of this hit
				vectors.Visit(field, value)
				if _, ok := next.Locations[field]; ok && highlightOn {
					if _, ok := query.Highlight.Fields[field]; !ok {
						return true
					}
					start := len(storedValues)
					storedValues = append(storedValues, value...)
					storedFields = append(storedFields, storedField{name: field, start: start, end: len(storedValues)})
				}
			}

			return true
		})
		if err != nil {
			log.Printf("core.SearchV2: error accessing stored fields: %s", err.Error())
			continue
		}
		if hasSource {
			value, rerr := vectors.Restore(storedSource)
			if rerr != nil {
				log.Printf("core.SearchV2: error restoring _source vectors of [%s]: %s", id, rerr.Error())
				value = storedSource
			}
			sourceData = source.Response(query.Source.(*meta.Source), value)
			if query.Fields != nil {
				m := mappings
				if index, ok := indexes[indexName]; ok {
					m = index.GetMappings()
				}
				fieldsData = fields.Response(query.Fields.([]*meta.Field), value, m)
			}
		}
		if highlightOn {
			h := highlightFor(indexName)
			// an array stores one value per element, each highlighted on its own
			for _, f := range storedFields {
				prev, _ := highlightData[f.name].([]string)
				if fragments := highlightField(next, h.analyzers[f.name], f.name, storedValues[f.start:f.end], len(prev)); len(fragments) > 0 {
					highlightData[f.name] = append(prev, fragments...)
				}
			}
			// as Elasticsearch, fields without a stored value are highlighted from _source
			if len(rawSource) > 0 && len(highlightData) < len(h.analyzers) {
				highlightFromSource(rawSource, h, next.Locations, highlightData, func(field string, value []byte, have int) []string {
					return highlightField(next, h.analyzers[field], field, value, have)
				})
			}
		}

		// documents of an index with _source disabled have no _source
		if sourceData != nil && !timestamp.IsZero() && (query.Source.(*meta.Source) == nil ||
			!query.Source.(*meta.Source).Enable || len(query.Source.(*meta.Source).Fields) == 0) {
			sourceData["@timestamp"] = timestamp
		}

		hit := meta.Hit{
			Index:     indexName,
			Type:      "_doc",
			ID:        id,
			Score:     next.Score,
			Timestamp: timestamp,
			Source:    sourceData,
			Fields:    fieldsData,
			Highlight: highlightData,
		}
		Hits = append(Hits, hit)

		next, err = dmi.Next()
	}
	if err != nil {
		log.Printf("core.SearchV2: error iterating results: %s", err.Error())
	}

	resp.Took = int(dmi.Aggregations().Duration().Milliseconds())
	resp.Shards = meta.Shards{Total: shardNum, Successful: readerNum, Skipped: shardNum - readerNum}
	resp.Hits = meta.Hits{
		Total:    meta.Total{Value: int(dmi.Aggregations().Count())},
		MaxScore: dmi.Aggregations().Metric("max_score"),
		Hits:     Hits,
	}

	if err := uquery.FormatResponse(resp, query, dmi.Aggregations()); err != nil {
		log.Printf("core.SearchV2: error format response: %s", err.Error())
	}

	return resp, nil
}

// highlightIndex holds the highlight settings of the index of hits.
type highlightIndex struct {
	mappings  *meta.Mappings
	analyzers map[string]*analysis.Analyzer // by highlighted field
}

// storedField locates the stored value of field name in a shared buffer.
type storedField struct {
	name       string
	start, end int
}

// highlightFromSource highlights each matched field of h missing in data
// with the values of the field in the _source document raw. highlightField
// is given the number of fragments the field has already.
func highlightFromSource(raw []byte, h *highlightIndex, matched search.FieldTermLocationMap,
	data map[string]interface{}, highlightField func(field string, value []byte, have int) []string) {
	missing := false
	for field := range h.analyzers {
		if _, ok := data[field]; !ok && matched[field] != nil {
			missing = true
			break
		}
	}
	if !missing {
		return
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		log.Printf("core.SearchV2: error decoding _source for highlight: %s", err.Error())
		return
	}
	for field := range h.analyzers {
		if _, ok := data[field]; ok || matched[field] == nil {
			continue
		}
		prop, _ := h.mappings.GetProperty(field)
		terms := prop.Type == "text" || prop.Type == "keyword"
		var fragments []string
		for _, value := range sourceStrings(doc, sourceField(h.mappings, field), terms) {
			fragments = append(fragments, highlightField(field, []byte(value), len(fragments))...)
		}
		if len(fragments) > 0 {
			data[field] = fragments
		}
	}
}

// sourceField returns the _source path of field: a multi-field such as
// "body.raw" indexes the value of its parent "body".
func sourceField(mappings *meta.Mappings, field string) string {
	i := strings.LastIndexByte(field, '.')
	if i <= 0 {
		return field
	}
	if parent, ok := mappings.GetProperty(field[:i]); ok {
		if _, ok := parent.Fields[field[i+1:]]; ok {
			return field[:i]
		}
	}
	return field
}

// sourceStrings returns the string values of the dotted field path in doc,
// either a string or the strings of an array. For terms, a text or keyword
// field, numbers and booleans count too, as the string the writer indexes.
func sourceStrings(doc map[string]interface{}, field string, terms bool) []string {
	indexed := func(v interface{}) (string, bool) {
		switch v := v.(type) {
		case string:
			return v, true
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), terms
		case bool:
			return strconv.FormatBool(v), terms
		}
		return "", false
	}
	v, _ := sourceValue(doc, field)
	if array, ok := v.([]interface{}); ok {
		values := make([]string, 0, len(array))
		for _, e := range array {
			if s, ok := indexed(e); ok {
				values = append(values, s)
			}
		}
		return values
	}
	if s, ok := indexed(v); ok {
		return []string{s}
	}
	return nil
}

// sourceValue returns the value at the dotted field path in v. As flatten
// names them, objects are descended by key and arrays by numeric index.
func sourceValue(v interface{}, field string) (interface{}, bool) {
	switch v := v.(type) {
	case map[string]interface{}:
		if e, ok := v[field]; ok {
			return e, true
		}
		for i := len(field) - 1; i > 0; i-- {
			if field[i] != '.' {
				continue
			}
			if sub, ok := v[field[:i]]; ok {
				if e, ok := sourceValue(sub, field[i+1:]); ok {
					return e, true
				}
			}
		}
	case []interface{}:
		key, rest, nested := strings.Cut(field, ".")
		n, err := strconv.Atoi(key)
		if err != nil || n < 0 || n >= len(v) {
			return nil, false
		}
		if !nested {
			return v[n], true
		}
		return sourceValue(v[n], rest)
	}
	return nil, false
}

// highlightFieldAnalyzers returns the index analyzer of each highlighted
// text field, nil for keywords, to find the offsets of matched terms.
func highlightFieldAnalyzers(h *meta.Highlight, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) map[string]*analysis.Analyzer {
	if h == nil || len(h.Fields) == 0 {
		return nil
	}
	zers := make(map[string]*analysis.Analyzer, len(h.Fields))
	for field := range h.Fields {
		if prop, ok := mappings.GetProperty(field); ok && prop.Type != "text" {
			zers[field] = nil
			continue
		}
		zer, _ := zedanalysis.QueryAnalyzerForField(analyzers, mappings, field)
		if zer == nil {
			zer = analyzer.NewStandardAnalyzer()
		}
		zers[field] = zer
	}
	return zers
}

// rewriteJoinQuery resolves nested and join clauses of query and hides nested
// documents from results.
//
// Riot has no block join support: nested array elements and join children are
// indexed as own documents, so `nested`, `has_child` and `has_parent` clauses
// are resolved by extra search over same readers, and result is pushed back
// into query as list of document ids (see uquery.Rewrite).
func rewriteJoinQuery(query *meta.ZedQuery, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer, readers []*riot.Reader) error {
	paths := nestedPaths(mappings)
	if len(paths) == 0 {
		if _, ok := mappings.GetProperty(meta.JoinNameField); !ok {
			return nil // no nested or join mapping, nothing to rewrite
		}
	}

	exec := func(sub map[string]interface{}, collectField string) ([]string, error) {
		return collectFieldValues(readers, sub, collectField, mappings, analyzers)
	}
	rewritten, err := uquery.Rewrite(query.Query, exec)
	if err != nil {
		return err
	}

	query.Query = excludeNestedDocuments(rewritten, paths)

	return nil
}

// excludeNestedDocuments wraps query so that hidden nested documents are
// neither returned nor counted in hits.total.
func excludeNestedDocuments(query interface{}, paths []string) interface{} {
	if len(paths) == 0 {
		return query
	}
	if query == nil {
		query = map[string]interface{}{"match_all": map[string]interface{}{}}
	}
	return map[string]interface{}{
		"bool": map[string]interface{}{
			"must": query,
			"must_not": map[string]interface{}{
				"terms": map[string]interface{}{meta.NestedPathField: paths},
			},
		},
	}
}

// collectFieldValues runs sub query over readers and returns value of field
// for every match, one entry per match. Hidden documents are not excluded,
// sub query searches them on purpose.
func collectFieldValues(readers []*riot.Reader, sub map[string]interface{}, field string, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) ([]string, error) {
	subQuery, err := uquerydsl.Query(sub, mappings, analyzers)
	if err != nil {
		return nil, err
	}

	limit := uquery.RewriteLimit() + 1
	ctx := context.Background()
	values := make([]string, 0, 16)
	for _, reader := range readers {
		request := riot.NewTopNSearch(limit, subQuery)
		if field != "_id" {
			// sorting by collected field makes collector load its doc values
			request.SortBy([]string{field})
		}
		dmi, err := reader.Search(ctx, request)
		if err != nil {
			return nil, err
		}
		next, err := dmi.Next()
		for err == nil && next != nil {
			if field == "_id" {
				var id string
				if verr := next.VisitStoredFields(func(name string, value []byte) bool {
					if name == "_id" {
						id = string(value)
						return false
					}
					return true
				}); verr != nil {
					return nil, verr
				}
				if id != "" {
					values = append(values, id)
				}
			} else {
				for _, value := range next.DocValues(field) {
					values = append(values, string(value))
				}
			}
			next, err = dmi.Next()
		}
		if err != nil {
			return nil, err
		}
	}

	return values, nil
}
