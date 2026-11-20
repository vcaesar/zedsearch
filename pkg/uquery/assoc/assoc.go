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

// Package assoc implements the Elasticsearch "associated terms" features
// (more_like_this, suggesters, significant_terms) as pure functions over riot
// readers.
package assoc

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/aggregations"

	"github.com/vcaesar/zedsearch/pkg/meta"
	zedanalysis "github.com/vcaesar/zedsearch/pkg/uquery/analysis"
	zedanalyzer "github.com/vcaesar/zedsearch/pkg/uquery/analysis/analyzer"
	"github.com/vcaesar/zedsearch/pkg/zutils"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// totalDocs is the number of live documents of all readers.
func totalDocs(readers []*riot.Reader) (uint64, error) {
	var n uint64
	for _, r := range readers {
		c, err := r.Count()
		if err != nil {
			return 0, err
		}
		n += c
	}
	return n, nil
}

// iterateDict calls fn with every term of field in [start, end) of every
// reader and its doc freq in that reader. A nil end iterates to the last term.
func iterateDict(ctx context.Context, readers []*riot.Reader, field string, start, end []byte, fn func(term string, count uint64)) error {
	for _, r := range readers {
		if err := ctx.Err(); err != nil {
			return err
		}
		it, err := r.DictionaryIterator(field, nil, start, end)
		if err != nil {
			return err
		}
		for e, err := it.Next(); e != nil || err != nil; e, err = it.Next() {
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				_ = it.Close()
				return err
			}
			fn(e.Term(), e.Count())
		}
		if err := it.Close(); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// dictTerms returns the terms of field in [start, end). Their counts are not
// returned: ice (v1.6.1) keeps the 1-hit state of the previous term while
// iterating, so every count after a single-document term reads 1. Use
// docFreq, whose single-term iteration is exact.
func dictTerms(ctx context.Context, readers []*riot.Reader, field string, start, end []byte) (map[string]struct{}, error) {
	rv := make(map[string]struct{})
	err := iterateDict(ctx, readers, field, start, end, func(term string, _ uint64) {
		rv[term] = struct{}{}
	})
	return rv, err
}

// docFreq is the number of documents holding term in field.
func docFreq(ctx context.Context, readers []*riot.Reader, field, term string) (uint64, error) {
	var n uint64
	err := iterateDict(ctx, readers, field, []byte(term), append([]byte(term), 0), func(t string, count uint64) {
		if t == term {
			n += count
		}
	})
	return n, err
}

// prefixTerms returns the terms of field starting with prefix.
func prefixTerms(ctx context.Context, readers []*riot.Reader, field, prefix string) (map[string]struct{}, error) {
	if prefix == "" {
		return dictTerms(ctx, readers, field, nil, nil)
	}
	return dictTerms(ctx, readers, field, []byte(prefix), prefixEnd([]byte(prefix)))
}

// prefixEnd is the first key after all keys starting with prefix, nil if
// there is none.
func prefixEnd(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xff {
			end[i]++
			return end[:i+1]
		}
	}
	return nil
}

// countMatches is the number of documents matching q in all readers.
func countMatches(ctx context.Context, readers []*riot.Reader, q riot.Query) (uint64, error) {
	var n uint64
	for _, r := range readers {
		req := riot.NewTopNSearch(0, q)
		req.AddAggregation("count", aggregations.CountMatches())
		dmi, err := r.Search(ctx, req)
		if err != nil {
			return 0, err
		}
		n += dmi.Aggregations().Count()
	}
	return n, nil
}

// storedDoc is a document loaded from the stored fields of a reader.
type storedDoc struct {
	ID     string
	Index  string
	Source map[string]interface{}
}

// searchDocs returns up to size documents matching q, in reader order.
func searchDocs(ctx context.Context, readers []*riot.Reader, q riot.Query, size int) ([]storedDoc, error) {
	var docs []storedDoc
	for _, r := range readers {
		if len(docs) >= size {
			break
		}
		dmi, err := r.Search(ctx, riot.NewTopNSearch(size-len(docs), q))
		if err != nil {
			return nil, err
		}
		for m, err := dmi.Next(); m != nil || err != nil; m, err = dmi.Next() {
			if err != nil {
				return nil, err
			}
			doc, err := loadDoc(m)
			if err != nil {
				return nil, err
			}
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

func loadDoc(m *search.DocumentMatch) (storedDoc, error) {
	var doc storedDoc
	var source []byte
	err := m.VisitStoredFields(func(field string, value []byte) bool {
		switch field {
		case "_id":
			doc.ID = string(value)
		case "_index":
			doc.Index = string(value)
		case "_source":
			source = append([]byte(nil), value...)
		}
		return true
	})
	if err != nil {
		return doc, err
	}
	if len(source) > 0 {
		if err := json.Unmarshal(source, &doc.Source); err != nil {
			return doc, err
		}
	}
	return doc, nil
}

// fieldAnalyzer returns the analyzer for field: the named analyzer, nil
// (raw value) for keyword fields, else the field's index or search analyzer
// with the standard analyzer as fallback.
func fieldAnalyzer(analyzers map[string]*analysis.Analyzer, mappings *meta.Mappings, field, name string, searching bool) (*analysis.Analyzer, error) {
	if name != "" {
		return zedanalysis.QueryAnalyzer(analyzers, name)
	}
	if prop, ok := mappings.GetProperty(field); ok && prop.Type != "text" {
		return nil, nil
	}
	index, query := zedanalysis.QueryAnalyzerForField(analyzers, mappings, field)
	if searching && query != nil {
		return query, nil
	}
	if index != nil {
		return index, nil
	}
	return zedanalyzer.NewStandardAnalyzer(nil)
}

// tokenize analyzes text, a nil analyzer keeps text as a single token.
func tokenize(ana *analysis.Analyzer, text string) analysis.TokenStream {
	if ana == nil {
		if text == "" {
			return nil
		}
		return analysis.TokenStream{{Term: []byte(text), Start: 0, End: len(text), PositionIncr: 1}}
	}
	return ana.Analyze([]byte(text))
}

// fieldValues returns the string values of field in a _source document. A
// dotted field is looked up as a path, a multi-field ("title.keyword") falls
// back to its parent.
func fieldValues(source map[string]interface{}, field string) []string {
	for path := field; path != ""; {
		if v, ok := lookupPath(source, path); ok {
			return flattenStrings(v, nil)
		}
		i := strings.LastIndexByte(path, '.')
		if i < 0 {
			break
		}
		path = path[:i]
	}
	return nil
}

// lookupPath returns the value at the dotted path in v: an object is
// descended by exact (possibly dotted) key, an array by numeric index.
func lookupPath(v interface{}, path string) (interface{}, bool) {
	switch v := v.(type) {
	case map[string]interface{}:
		if e, ok := v[path]; ok {
			return e, true
		}
		for i := len(path) - 1; i > 0; i-- {
			if path[i] != '.' {
				continue
			}
			if sub, ok := v[path[:i]]; ok {
				if e, ok := lookupPath(sub, path[i+1:]); ok {
					return e, true
				}
			}
		}
	case []interface{}:
		key, rest, nested := strings.Cut(path, ".")
		n, err := strconv.Atoi(key)
		if err != nil || n < 0 || n >= len(v) {
			return nil, false
		}
		if !nested {
			return v[n], true
		}
		return lookupPath(v[n], rest)
	}
	return nil, false
}

func flattenStrings(v interface{}, rv []string) []string {
	switch v := v.(type) {
	case nil, map[string]interface{}:
	case []interface{}:
		for _, vv := range v {
			rv = flattenStrings(vv, rv)
		}
	default:
		if s, err := zutils.ToString(v); err == nil {
			rv = append(rv, s)
		}
	}
	return rv
}

// runeOffset converts a byte offset of text into a character offset.
func runeOffset(text string, byteOffset int) int {
	return utf8.RuneCountInString(text[:min(byteOffset, len(text))])
}
