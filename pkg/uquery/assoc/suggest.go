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
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

type suggester struct {
	readers   []*riot.Reader
	mappings  *meta.Mappings
	analyzers map[string]*analysis.Analyzer
	numDocs   uint64
}

// suggestion is the input of one named suggestion.
type suggestion struct {
	text   string
	prefix *string
	regex  *string
}

// Suggest runs the "suggest" section of a search body and returns the
// Elasticsearch "suggest" response: {"name": [{text, offset, length, options}]}.
// It supports the term, phrase (simplified, see phraseSuggest) and
// completion suggesters.
func Suggest(readers []*riot.Reader, suggest map[string]interface{}, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) (map[string]interface{}, error) {
	if mappings == nil {
		mappings = meta.NewMappings()
	}
	numDocs, err := totalDocs(readers)
	if err != nil {
		return nil, err
	}
	s := &suggester{readers: readers, mappings: mappings, analyzers: analyzers, numDocs: numDocs}

	var global string
	if v, ok := suggest["text"]; ok {
		if global, err = paramString("suggest", "text", v); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(suggest))
	for name := range suggest {
		if name != "text" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	rv := make(map[string]interface{}, len(names))
	for _, name := range names {
		def, ok := suggest[name].(map[string]interface{})
		if !ok {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[suggest] suggestion [%s] should be an object", name))
		}
		in := suggestion{text: global}
		var kind string
		var opts map[string]interface{}
		for k, v := range def {
			switch k {
			case "text":
				if in.text, err = paramString(name, k, v); err != nil {
					return nil, err
				}
			case "prefix", "regex":
				str, err := paramString(name, k, v)
				if err != nil {
					return nil, err
				}
				if k == "prefix" {
					in.prefix = &str
				} else {
					in.regex = &str
				}
			case "term", "phrase", "completion":
				if kind != "" {
					return nil, errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[suggest] suggestion [%s] has more than one suggester", name))
				}
				kind = k
				if opts, ok = v.(map[string]interface{}); !ok {
					return nil, paramError(name, k, v)
				}
			default:
				return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[suggest] suggester [%s] not supported", k))
			}
		}

		var entries []interface{}
		switch kind {
		case "term":
			entries, err = s.termSuggest(in.text, opts)
		case "phrase":
			entries, err = s.phraseSuggest(in.text, opts)
		case "completion":
			entries, err = s.completionSuggest(in, opts)
		default:
			return nil, errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[suggest] suggestion [%s] requires a suggester", name))
		}
		if err != nil {
			return nil, err
		}
		rv[name] = entries
	}
	return rv, nil
}

// termOpts are the options of the term suggester and the phrase suggester
// direct_generator.
type termOpts struct {
	field         string
	analyzer      string
	size          int
	sort          string
	mode          string
	maxEdits      int
	prefixLength  int
	minWordLength int
	minDocFreq    float64
	maxTermFreq   float64
	accuracy      float64
}

func parseTermOpts(kind string, opts map[string]interface{}) (*termOpts, error) {
	o := &termOpts{size: 5, sort: "score", mode: "missing", maxEdits: 2, prefixLength: 1, minWordLength: 4, maxTermFreq: 0.01, accuracy: 0.5}
	var err error
	for k, v := range opts {
		switch k {
		case "field":
			o.field, err = paramString(kind, k, v)
		case "analyzer":
			o.analyzer, err = paramString(kind, k, v)
		case "size":
			o.size, err = paramInt(kind, k, v)
		case "sort":
			o.sort, err = paramString(kind, k, v)
			if err == nil && o.sort != "score" && o.sort != "frequency" {
				err = errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[%s] illegal sort [%s]", kind, o.sort))
			}
		case "suggest_mode":
			o.mode, err = paramString(kind, k, v)
			if err == nil && o.mode != "missing" && o.mode != "popular" && o.mode != "always" {
				err = errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[%s] illegal suggest_mode [%s]", kind, o.mode))
			}
		case "max_edits":
			o.maxEdits, err = paramInt(kind, k, v)
			if err == nil && (o.maxEdits < 1 || o.maxEdits > 2) {
				err = errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[%s] max_edits must be between 1 and 2", kind))
			}
		case "prefix_length", "prefix_len":
			o.prefixLength, err = paramInt(kind, k, v)
		case "min_word_length", "min_word_len":
			o.minWordLength, err = paramInt(kind, k, v)
		case "min_doc_freq":
			o.minDocFreq, err = paramFloat(kind, k, v)
		case "max_term_freq":
			o.maxTermFreq, err = paramFloat(kind, k, v)
		case "accuracy":
			o.accuracy, err = paramFloat(kind, k, v)
		case "string_distance", "max_inspections", "shard_size", "lowercase_terms", "pre_filter", "post_filter":
			// accepted, edits are always measured with Damerau-Levenshtein
		default:
			err = errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] unknown field [%s]", kind, k))
		}
		if err != nil {
			return nil, err
		}
	}
	if o.field == "" {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[%s] requires [field]", kind))
	}
	return o, nil
}

// threshold reads a frequency option, values below 1 are a fraction of the
// number of documents.
func (s *suggester) threshold(v float64) float64 {
	if v > 0 && v < 1 {
		return math.Ceil(v * float64(s.numDocs))
	}
	return v
}

type termOption struct {
	text  string
	score float64
	freq  uint64
}

func (s *suggester) termSuggest(text string, opts map[string]interface{}) ([]interface{}, error) {
	o, err := parseTermOpts("term", opts)
	if err != nil {
		return nil, err
	}
	ana, err := fieldAnalyzer(s.analyzers, s.mappings, o.field, o.analyzer, true)
	if err != nil {
		return nil, err
	}
	entries := make([]interface{}, 0)
	for _, token := range tokenize(ana, text) {
		term := string(token.Term)
		candidates, _, err := s.termCandidates(o, term)
		if err != nil {
			return nil, err
		}
		options := make([]interface{}, 0, len(candidates))
		for _, c := range candidates {
			options = append(options, map[string]interface{}{"text": c.text, "score": c.score, "freq": c.freq})
		}
		start := runeOffset(text, token.Start)
		entries = append(entries, map[string]interface{}{
			"text":    term,
			"offset":  start,
			"length":  runeOffset(text, token.End) - start,
			"options": options,
		})
	}
	return entries, nil
}

// termCandidates returns the corrections of term, like Lucene
// DirectSpellChecker, and the doc freq of term itself.
func (s *suggester) termCandidates(o *termOpts, term string) ([]termOption, uint64, error) {
	freq, err := docFreq(s.readers, o.field, term)
	if err != nil {
		return nil, 0, err
	}
	runes := []rune(term)
	if len(runes) < o.minWordLength || (o.mode == "missing" && freq > 0) {
		return nil, freq, nil
	}
	if maxFreq := s.threshold(o.maxTermFreq); freq > 0 && float64(freq) > maxFreq {
		return nil, freq, nil
	}

	prefix := string(runes[:min(max(o.prefixLength, 0), len(runes))])
	terms, err := prefixTerms(s.readers, o.field, prefix)
	if err != nil {
		return nil, 0, err
	}
	minFreq := s.threshold(o.minDocFreq)
	var rv []termOption
	for candidate := range terms {
		if candidate == term {
			continue
		}
		crunes := []rune(candidate)
		if abs(len(crunes)-len(runes)) > o.maxEdits {
			continue
		}
		d := editDistance(runes, crunes, true)
		if d > o.maxEdits {
			continue
		}
		score := 1 - float64(d)/float64(min(len(runes), len(crunes)))
		if score < o.accuracy {
			continue
		}
		cfreq, err := docFreq(s.readers, o.field, candidate)
		if err != nil {
			return nil, 0, err
		}
		if float64(cfreq) < minFreq || (o.mode == "popular" && cfreq <= freq) {
			continue
		}
		rv = append(rv, termOption{text: candidate, score: score, freq: cfreq})
	}
	sort.Slice(rv, func(i, j int) bool {
		a, b := rv[i], rv[j]
		if o.sort == "frequency" && a.freq != b.freq {
			return a.freq > b.freq
		}
		if a.score != b.score {
			return a.score > b.score
		}
		if a.freq != b.freq {
			return a.freq > b.freq
		}
		return a.text < b.text
	})
	if len(rv) > o.size {
		rv = rv[:o.size]
	}
	return rv, freq, nil
}

type phraseOpts struct {
	field, analyzer   string
	size              int
	maxErrors         float64
	separator         string
	preTag, postTag   string
	highlight         bool
	confidence        float64
	realWordLikelihod float64
	generator         map[string]interface{}
}

func parsePhraseOpts(opts map[string]interface{}) (*phraseOpts, error) {
	const kind = "phrase"
	o := &phraseOpts{size: 1, maxErrors: 1, separator: " ", confidence: 1, realWordLikelihod: 0.95}
	var err error
	for k, v := range opts {
		switch k {
		case "field":
			o.field, err = paramString(kind, k, v)
		case "analyzer":
			o.analyzer, err = paramString(kind, k, v)
		case "size":
			o.size, err = paramInt(kind, k, v)
		case "max_errors":
			o.maxErrors, err = paramFloat(kind, k, v)
		case "separator":
			o.separator, err = paramString(kind, k, v)
		case "confidence":
			o.confidence, err = paramFloat(kind, k, v)
		case "real_word_error_likelihood":
			o.realWordLikelihod, err = paramFloat(kind, k, v)
		case "highlight":
			h, ok := v.(map[string]interface{})
			if !ok {
				return nil, paramError(kind, k, v)
			}
			o.highlight = true
			if v, ok := h["pre_tag"]; ok {
				o.preTag, err = paramString(kind, "pre_tag", v)
			}
			if v, ok := h["post_tag"]; ok && err == nil {
				o.postTag, err = paramString(kind, "post_tag", v)
			}
		case "direct_generator":
			gens, ok := v.([]interface{})
			if !ok || len(gens) == 0 {
				return nil, paramError(kind, k, v)
			}
			// only the first generator is used
			if o.generator, ok = gens[0].(map[string]interface{}); !ok {
				return nil, paramError(kind, k, gens[0])
			}
		case "gram_size", "token_limit", "shard_size", "force_unigrams", "smoothing", "collate":
			// accepted, the simplified phrase suggester scores unigrams only
		default:
			err = errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] unknown field [%s]", kind, k))
		}
		if err != nil {
			return nil, err
		}
	}
	if o.field == "" {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException, "[phrase] requires [field]")
	}
	return o, nil
}

type phraseCandidate struct {
	terms     []string
	corrected []bool
	score     float64
	errors    int
}

// phraseSuggest is a simplified phrase suggester: every token of text may be
// replaced by one of its term suggestions (from the first direct_generator)
// and a phrase is scored by the product of its unigram probabilities
// (freq+1)/(docs+1), each weighted by real_word_error_likelihood for the
// original term or by the edit similarity for a correction. Phrases with at
// most max_errors corrections that score above confidence times the input
// phrase score are returned, there is no n-gram language model and no collate.
func (s *suggester) phraseSuggest(text string, opts map[string]interface{}) ([]interface{}, error) {
	o, err := parsePhraseOpts(opts)
	if err != nil {
		return nil, err
	}
	generator := map[string]interface{}{"field": o.field}
	for k, v := range o.generator {
		generator[k] = v
	}
	gen, err := parseTermOpts("direct_generator", generator)
	if err != nil {
		return nil, err
	}
	ana, err := fieldAnalyzer(s.analyzers, s.mappings, o.field, o.analyzer, true)
	if err != nil {
		return nil, err
	}
	tokens := tokenize(ana, text)

	maxErrors := int(o.maxErrors)
	if o.maxErrors < 1 {
		maxErrors = max(1, int(o.maxErrors*float64(len(tokens))))
	}
	prob := func(freq uint64) float64 { return float64(freq+1) / float64(s.numDocs+1) }
	beam := max(10*o.size, 50)
	states := []phraseCandidate{{score: 1}}
	inputScore := 1.0
	for _, token := range tokens {
		term := string(token.Term)
		corrections, freq, err := s.termCandidates(gen, term)
		if err != nil {
			return nil, err
		}
		original := o.realWordLikelihod * prob(freq)
		inputScore *= original
		next := make([]phraseCandidate, 0, len(states)*(len(corrections)+1))
		for _, st := range states {
			next = append(next, st.with(term, false, original))
			if st.errors >= maxErrors {
				continue
			}
			for _, c := range corrections {
				next = append(next, st.with(c.text, true, c.score*prob(c.freq)))
			}
		}
		sort.SliceStable(next, func(i, j int) bool { return next[i].score > next[j].score })
		states = next[:min(len(next), beam)]
	}

	options := make([]interface{}, 0, o.size)
	cutoff := o.confidence * inputScore
	for _, st := range states {
		if len(options) >= o.size {
			break
		}
		if st.errors == 0 || st.score <= cutoff {
			continue
		}
		option := map[string]interface{}{"text": strings.Join(st.terms, o.separator), "score": st.score}
		if o.highlight {
			marked := make([]string, len(st.terms))
			for i, t := range st.terms {
				if st.corrected[i] {
					t = o.preTag + t + o.postTag
				}
				marked[i] = t
			}
			option["highlighted"] = strings.Join(marked, o.separator)
		}
		options = append(options, option)
	}
	return []interface{}{map[string]interface{}{
		"text":    text,
		"offset":  0,
		"length":  utf8.RuneCountInString(text),
		"options": options,
	}}, nil
}

func (c phraseCandidate) with(term string, corrected bool, weight float64) phraseCandidate {
	rv := phraseCandidate{
		terms:     append(append(make([]string, 0, len(c.terms)+1), c.terms...), term),
		corrected: append(append(make([]bool, 0, len(c.corrected)+1), c.corrected...), corrected),
		score:     c.score * weight,
		errors:    c.errors,
	}
	if corrected {
		rv.errors++
	}
	return rv
}

type completionOpts struct {
	field          string
	size           int
	skipDuplicates bool
	fuzzy          bool
	fuzziness      interface{}
	transpositions bool
	minLength      int
	prefixLength   int
}

func parseCompletionOpts(opts map[string]interface{}) (*completionOpts, error) {
	const kind = "completion"
	o := &completionOpts{size: 5, fuzziness: "AUTO", transpositions: true, minLength: 3, prefixLength: 1}
	var err error
	for k, v := range opts {
		switch k {
		case "field":
			o.field, err = paramString(kind, k, v)
		case "size":
			o.size, err = paramInt(kind, k, v)
		case "skip_duplicates":
			o.skipDuplicates, err = paramBool(kind, k, v)
		case "fuzzy":
			err = o.parseFuzzy(v)
		case "contexts":
			err = errors.New(errors.ErrorTypeNotImplemented, "[completion] contexts are not supported")
		case "analyzer", "regex":
			// accepted: keyword fields match raw values, text fields lowercase
		default:
			err = errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] unknown field [%s]", kind, k))
		}
		if err != nil {
			return nil, err
		}
	}
	if o.field == "" {
		return nil, errors.New(errors.ErrorTypeIllegalArgumentException, "[completion] requires [field]")
	}
	return o, nil
}

func (o *completionOpts) parseFuzzy(v interface{}) error {
	const kind = "completion.fuzzy"
	switch v := v.(type) {
	case bool:
		o.fuzzy = v
		return nil
	case map[string]interface{}:
		o.fuzzy = true
		var err error
		for k, vv := range v {
			switch k {
			case "fuzziness":
				o.fuzziness = vv
			case "transpositions":
				o.transpositions, err = paramBool(kind, k, vv)
			case "min_length":
				o.minLength, err = paramInt(kind, k, vv)
			case "prefix_length":
				o.prefixLength, err = paramInt(kind, k, vv)
			case "unicode_aware":
			default:
				err = errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] unknown field [%s]", kind, k))
			}
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return paramError("completion", "fuzzy", v)
	}
}

// maxEdits resolves the fuzziness for an input of n characters.
func (o *completionOpts) maxEdits(n int) (int, error) {
	if !o.fuzzy || n < o.minLength {
		return 0, nil
	}
	if s, ok := o.fuzziness.(string); ok && strings.EqualFold(s, "AUTO") {
		switch {
		case n < 3:
			return 0, nil
		case n < 6:
			return 1, nil
		default:
			return 2, nil
		}
	}
	f, err := paramInt("completion.fuzzy", "fuzziness", o.fuzziness)
	if err != nil {
		return 0, err
	}
	if f < 0 || f > 2 {
		return 0, errors.New(errors.ErrorTypeIllegalArgumentException, "[completion] fuzziness must be between 0 and 2")
	}
	return f, nil
}

type completionMatch struct {
	term  string
	score float64
}

// completionSuggest matches the prefix (or regex) against the dictionary
// terms of field: raw values for keyword fields, lowercased for text fields
// whose terms are single tokens. A match scores 1/(1+edits) and returns one
// option per matching document, one per term with skip_duplicates.
func (s *suggester) completionSuggest(in suggestion, opts map[string]interface{}) ([]interface{}, error) {
	o, err := parseCompletionOpts(opts)
	if err != nil {
		return nil, err
	}
	input := in.text
	if in.prefix != nil {
		input = *in.prefix
	}
	if in.regex != nil {
		input = *in.regex
	}
	needle := input
	if prop, ok := s.mappings.GetProperty(o.field); !ok || prop.Type == "text" {
		needle = strings.ToLower(input)
	}

	var matches []completionMatch
	switch {
	case in.regex != nil:
		re, err := regexp.Compile("^(?:" + needle + ")$")
		if err != nil {
			return nil, errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[completion] invalid regex [%s]", input)).Cause(err)
		}
		terms, err := prefixTerms(s.readers, o.field, "")
		if err != nil {
			return nil, err
		}
		for term := range terms {
			if re.MatchString(term) {
				matches = append(matches, completionMatch{term: term, score: 1})
			}
		}
	default:
		runes := []rune(needle)
		edits, err := o.maxEdits(len(runes))
		if err != nil {
			return nil, err
		}
		prefix := needle
		if edits > 0 {
			prefix = string(runes[:min(max(o.prefixLength, 0), len(runes))])
		}
		terms, err := prefixTerms(s.readers, o.field, prefix)
		if err != nil {
			return nil, err
		}
		for term := range terms {
			d := 0
			if edits > 0 {
				if d = prefixDistance(runes, []rune(term), o.transpositions); d > edits {
					continue
				}
			}
			matches = append(matches, completionMatch{term: term, score: 1 / float64(1+d)})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].term < matches[j].term
	})

	options := make([]interface{}, 0, o.size)
	for _, m := range matches {
		if len(options) >= o.size {
			break
		}
		limit := o.size - len(options)
		if o.skipDuplicates {
			limit = 1
		}
		docs, err := searchDocs(s.readers, riot.NewTermQuery(m.term).SetField(o.field), limit)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			option := map[string]interface{}{"text": m.term, "_index": doc.Index, "_id": doc.ID, "_score": m.score}
			if doc.Source != nil {
				option["_source"] = doc.Source
			}
			options = append(options, option)
		}
	}
	return []interface{}{map[string]interface{}{
		"text":    input,
		"offset":  0,
		"length":  utf8.RuneCountInString(input),
		"options": options,
	}}, nil
}

// distanceMatrix is the optimal string alignment (Damerau-Levenshtein with
// adjacent transpositions) distance matrix of a against b.
func distanceMatrix(a, b []rune, transpositions bool) [][]int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if transpositions && i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d
}

func editDistance(a, b []rune, transpositions bool) int {
	return distanceMatrix(a, b, transpositions)[len(a)][len(b)]
}

// prefixDistance is the smallest edit distance between prefix and any prefix
// of term.
func prefixDistance(prefix, term []rune, transpositions bool) int {
	last := distanceMatrix(prefix, term, transpositions)[len(prefix)]
	rv := last[0]
	for _, d := range last[1:] {
		rv = min(rv, d)
	}
	return rv
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
