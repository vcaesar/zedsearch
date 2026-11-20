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

package assoc

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

const mltKind = "more_like_this"

// mltItem is one like / unlike entry: free text, an artificial document or a
// document of the index by id.
type mltItem struct {
	text  *string
	doc   map[string]interface{}
	id    string
	index string
}

type mltParams struct {
	fields             []string
	like, unlike       []mltItem
	minTermFreq        int
	maxQueryTerms      int
	minDocFreq         int
	maxDocFreq         int
	minWordLength      int
	maxWordLength      int
	stopWords          map[string]struct{}
	minimumShouldMatch interface{}
	boostTerms         float64
	include            bool
	boost              float64
	analyzer           string
	failOnUnsupported  bool
}

type mltTerm struct {
	field string
	term  string
	score float64
}

// MoreLikeThis rewrites the body of a {"more_like_this": body} query into a
// plain bool query of the most interesting terms of the liked texts and
// documents, scored by tf*idf as Lucene MoreLikeThis does. It returns
// {"match_none": {}} when no term qualifies.
func MoreLikeThis(ctx context.Context, readers []*riot.Reader, body map[string]interface{}, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) (map[string]interface{}, error) {
	if mappings == nil {
		mappings = meta.NewMappings()
	}
	p, err := parseMLT(body)
	if err != nil {
		return nil, err
	}
	fields, err := mltFields(p, mappings)
	if err != nil {
		return nil, err
	}

	var likeIDs []interface{}
	like, err := resolveItems(ctx, readers, p.like, &likeIDs)
	if err != nil {
		return nil, err
	}
	unlike, err := resolveItems(ctx, readers, p.unlike, nil)
	if err != nil {
		return nil, err
	}

	numDocs, err := totalDocs(readers)
	if err != nil {
		return nil, err
	}
	var terms []mltTerm
	for _, field := range fields {
		ana, err := fieldAnalyzer(analyzers, mappings, field, p.analyzer, false)
		if err != nil {
			return nil, err
		}
		freqs := p.termFreqs(ana, field, like)
		skip := p.termFreqs(ana, field, unlike)
		for term, tf := range freqs {
			if _, ok := skip[term]; ok {
				continue
			}
			if p.minTermFreq > 0 && tf < p.minTermFreq {
				continue
			}
			df, err := docFreq(ctx, readers, field, term)
			if err != nil {
				return nil, err
			}
			if df == 0 || (p.minDocFreq > 0 && df < uint64(p.minDocFreq)) || (p.maxDocFreq > 0 && df > uint64(p.maxDocFreq)) {
				continue
			}
			idf := 1 + math.Log(float64(numDocs)/float64(df+1))
			terms = append(terms, mltTerm{field: field, term: term, score: float64(tf) * idf})
		}
	}
	if len(terms) == 0 {
		return map[string]interface{}{"match_none": map[string]interface{}{}}, nil
	}

	sort.Slice(terms, func(i, j int) bool {
		if terms[i].score != terms[j].score {
			return terms[i].score > terms[j].score
		}
		if terms[i].field != terms[j].field {
			return terms[i].field < terms[j].field
		}
		return terms[i].term < terms[j].term
	})
	if len(terms) > p.maxQueryTerms {
		terms = terms[:p.maxQueryTerms]
	}

	// bool has no boost of its own: the query boost scales every clause
	should := make([]interface{}, 0, len(terms))
	for _, t := range terms {
		boost := p.boost
		if p.boostTerms > 0 {
			boost *= p.boostTerms * t.score / terms[0].score
		}
		value := map[string]interface{}{"value": t.term}
		if boost != 1 {
			value["boost"] = boost
		}
		should = append(should, map[string]interface{}{"term": map[string]interface{}{t.field: value}})
	}
	boolQuery := map[string]interface{}{
		"should":               should,
		"minimum_should_match": p.minimumShouldMatch,
	}
	if !p.include && len(likeIDs) > 0 {
		boolQuery["must_not"] = []interface{}{
			map[string]interface{}{"ids": map[string]interface{}{"values": likeIDs}},
		}
	}
	return map[string]interface{}{"bool": boolQuery}, nil
}

func parseMLT(body map[string]interface{}) (*mltParams, error) {
	p := &mltParams{
		minTermFreq:        2,
		maxQueryTerms:      25,
		minDocFreq:         5,
		minimumShouldMatch: "30%",
		boost:              1,
		failOnUnsupported:  true,
	}
	var err error
	hasLike := false
	for k, v := range body {
		switch strings.ToLower(k) {
		case "fields":
			p.fields, err = paramStrings(mltKind, k, v)
		case "like":
			hasLike = true
			p.like, err = parseItems(v)
		case "unlike":
			p.unlike, err = parseItems(v)
		case "min_term_freq":
			p.minTermFreq, err = paramInt(mltKind, k, v)
		case "max_query_terms":
			p.maxQueryTerms, err = paramInt(mltKind, k, v)
		case "min_doc_freq":
			p.minDocFreq, err = paramInt(mltKind, k, v)
		case "max_doc_freq":
			p.maxDocFreq, err = paramInt(mltKind, k, v)
		case "min_word_length":
			p.minWordLength, err = paramInt(mltKind, k, v)
		case "max_word_length":
			p.maxWordLength, err = paramInt(mltKind, k, v)
		case "stop_words":
			var words []string
			words, err = paramStrings(mltKind, k, v)
			p.stopWords = make(map[string]struct{}, len(words))
			for _, w := range words {
				p.stopWords[w] = struct{}{}
			}
		case "minimum_should_match":
			p.minimumShouldMatch = v
		case "boost_terms":
			p.boostTerms, err = paramFloat(mltKind, k, v)
		case "include":
			p.include, err = paramBool(mltKind, k, v)
		case "boost":
			p.boost, err = paramFloat(mltKind, k, v)
		case "analyzer":
			p.analyzer, err = paramString(mltKind, k, v)
		case "fail_on_unsupported_field":
			p.failOnUnsupported, err = paramBool(mltKind, k, v)
		case "_name":
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] query does not support [%s]", mltKind, k))
		}
		if err != nil {
			return nil, err
		}
	}
	if !hasLike || len(p.like) == 0 {
		return nil, errors.New(errors.ErrorTypeParsingException, "[more_like_this] requires 'like' to be specified")
	}
	if p.maxQueryTerms <= 0 {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException, "[more_like_this] requires 'max_query_terms' to be greater than 0")
	}
	return p, nil
}

func parseItems(v interface{}) ([]mltItem, error) {
	switch v := v.(type) {
	case string:
		return []mltItem{{text: &v}}, nil
	case []interface{}:
		var rv []mltItem
		for _, vv := range v {
			items, err := parseItems(vv)
			if err != nil {
				return nil, err
			}
			rv = append(rv, items...)
		}
		return rv, nil
	case map[string]interface{}:
		var item mltItem
		for k, vv := range v {
			var err error
			switch k {
			case "doc":
				if item.doc, _ = vv.(map[string]interface{}); item.doc == nil {
					err = paramError(mltKind, k, vv)
				}
			case "_id":
				item.id, err = paramString(mltKind, k, vv)
			case "_index":
				item.index, err = paramString(mltKind, k, vv)
			case "routing", "_routing", "fields", "per_field_analyzer", "version", "version_type":
			default:
				err = errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] unknown field [%s] in like item", mltKind, k))
			}
			if err != nil {
				return nil, err
			}
		}
		if item.doc == nil && item.id == "" {
			return nil, errors.New(errors.ErrorTypeParsingException, "[more_like_this] like item requires either [_id] or [doc]")
		}
		return []mltItem{item}, nil
	default:
		return nil, paramError(mltKind, "like", v)
	}
}

// mltFields returns the requested fields, or every text and keyword field.
func mltFields(p *mltParams, mappings *meta.Mappings) ([]string, error) {
	if len(p.fields) == 0 {
		var fields []string
		for field, prop := range mappings.ListProperty() {
			if (prop.Type == "text" || prop.Type == "keyword") && !strings.HasPrefix(field, "_") && field != meta.TimeFieldName {
				fields = append(fields, field)
			}
		}
		sort.Strings(fields)
		return fields, nil
	}
	fields := make([]string, 0, len(p.fields))
	for _, field := range p.fields {
		if prop, ok := mappings.GetProperty(field); ok && prop.Type != "text" && prop.Type != "keyword" {
			if p.failOnUnsupported {
				return nil, errors.New(errors.ErrorTypeIllegalArgumentException,
					fmt.Sprintf("[more_like_this] only supports text/keyword fields: [%s] is [%s]", field, prop.Type))
			}
			continue
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// resolveItems loads the _source of the items liked by id, of their _index
// when set. Missing documents are skipped as in Elasticsearch. When ids is
// not nil, it gets the liked ids to exclude: the rewritten query can only
// exclude by _id, so an id is left out when it is also the id of a document
// of another index that is not liked.
func resolveItems(ctx context.Context, readers []*riot.Reader, items []mltItem, ids *[]interface{}) ([]mltItem, error) {
	// the documents with an id, at most one per reader
	byID := make(map[string][]storedDoc)
	docsByID := func(id string) ([]storedDoc, error) {
		if docs, ok := byID[id]; ok {
			return docs, nil
		}
		docs, err := searchDocs(ctx, readers, riot.NewTermQuery(id).SetField("_id"), len(readers))
		byID[id] = docs
		return docs, err
	}

	rv := make([]mltItem, 0, len(items))
	var liked []storedDoc
	for _, item := range items {
		if item.id == "" {
			rv = append(rv, item)
			continue
		}
		if item.doc == nil {
			docs, err := docsByID(item.id)
			if err != nil {
				return nil, err
			}
			i := slices.IndexFunc(docs, func(d storedDoc) bool { return sameIndex(item.index, d.Index) })
			if i < 0 {
				continue
			}
			item.doc = docs[i].Source
		}
		rv = append(rv, item)
		liked = append(liked, storedDoc{ID: item.id, Index: item.index})
	}
	if ids == nil {
		return rv, nil
	}
	isLiked := func(d storedDoc) bool {
		return slices.ContainsFunc(liked, func(l storedDoc) bool { return l.ID == d.ID && sameIndex(l.Index, d.Index) })
	}
	for _, l := range liked {
		if slices.Contains(*ids, interface{}(l.ID)) {
			continue
		}
		docs, err := docsByID(l.ID)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(docs, func(d storedDoc) bool { return !isLiked(d) }) {
			*ids = append(*ids, l.ID)
		}
	}
	return rv, nil
}

// sameIndex reports a document of index doc matching a requested index,
// an empty one matches any.
func sameIndex(want, doc string) bool {
	return want == "" || doc == "" || want == doc
}

// termFreqs counts the terms of field over all items, noise words excluded.
func (p *mltParams) termFreqs(ana *analysis.Analyzer, field string, items []mltItem) map[string]int {
	rv := make(map[string]int)
	add := func(text string) {
		for _, token := range tokenize(ana, text) {
			term := string(token.Term)
			if !p.isNoise(term) {
				rv[term]++
			}
		}
	}
	for _, item := range items {
		if item.text != nil {
			add(*item.text)
			continue
		}
		for _, v := range fieldValues(item.doc, field) {
			add(v)
		}
	}
	return rv
}

func (p *mltParams) isNoise(term string) bool {
	n := utf8.RuneCountInString(term)
	if n == 0 || n < p.minWordLength || (p.maxWordLength > 0 && n > p.maxWordLength) {
		return true
	}
	_, ok := p.stopWords[term]
	return ok
}
