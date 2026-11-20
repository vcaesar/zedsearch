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

package token

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// SynonymSetLookup returns the rules of a stored synonyms set. It is set by
// the package owning the synonyms set storage.
var SynonymSetLookup func(id string) ([]string, error)

const synonymKeySep = "\x00"

// SynonymTokenFilter replaces token sequences matching a synonym rule with
// their synonyms, alternates stacked at the same position.
type SynonymTokenFilter struct {
	groups     []synonymGroup        // parsed rules, kept to rebuild rules on Normalized
	rules      map[string][][]string // matched terms joined by synonymKeySep -> replacements
	maxLen     int
	ignoreCase bool
	expand     bool
}

// synonymGroup is a parsed rule: terms mapped to replacements, or
// equivalent terms when to is nil.
type synonymGroup struct {
	from, to [][]string
}

type positionedToken struct {
	pos int
	tok *analysis.Token
}

// synonymRule is a rule line and how errors refer to it: the rule itself
// when inline, its line number when read from synonyms_path.
type synonymRule struct {
	text, label string
}

// NewSynonymTokenFilter builds the synonym and synonym_graph token filters.
func NewSynonymTokenFilter(options interface{}) (analysis.TokenFilter, error) {
	lines, err := synonymLines(options)
	if err != nil {
		return nil, err
	}

	f := &SynonymTokenFilter{
		ignoreCase: synonymBool(options, "ignore_case", false),
		expand:     synonymBool(options, "expand", true),
	}
	lenient := synonymBool(options, "lenient", false)
	format, _ := zutils.GetStringFromMap(options, "format")
	switch strings.ToLower(format) {
	case "", "solr":
		for _, line := range lines {
			from, to, err := parseSolrRule(line.text, line.label)
			if err != nil {
				if lenient {
					continue
				}
				return nil, err
			}
			f.groups = append(f.groups, synonymGroup{from: from, to: to})
		}
	case "wordnet":
		groups, err := parseWordnet(lines, lenient)
		if err != nil {
			return nil, err
		}
		for _, words := range groups {
			terms, err := parseSolrTerms(words, strings.Join(words, ", "))
			if err != nil {
				if lenient {
					continue
				}
				return nil, err
			}
			f.groups = append(f.groups, synonymGroup{from: terms})
		}
	default:
		return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[synonym] unsupported format [%s]", format))
	}

	f.build(nil)
	return f, nil
}

// Normalized returns a copy of f whose rule terms are first run through
// the token filters preceding it in an analyzer, as Elasticsearch does.
func (f *SynonymTokenFilter) Normalized(pre []analysis.TokenFilter) *SynonymTokenFilter {
	n := &SynonymTokenFilter{groups: f.groups, ignoreCase: f.ignoreCase, expand: f.expand}
	n.build(pre)
	return n
}

// build indexes the rule groups, their terms run through pre. Terms
// emptied by pre are dropped, as are rules left without either side.
func (f *SynonymTokenFilter) build(pre []analysis.TokenFilter) {
	f.rules = make(map[string][][]string)
	for _, g := range f.groups {
		from := f.normalize(g.from, pre)
		if len(from) == 0 {
			continue
		}
		if g.to == nil {
			f.addEquivalent(from, f.expand)
			continue
		}
		to := f.normalize(g.to, pre)
		if len(to) == 0 {
			continue
		}
		for _, terms := range from {
			f.add(terms, to)
		}
	}
}

func (f *SynonymTokenFilter) normalize(terms [][]string, pre []analysis.TokenFilter) [][]string {
	out := make([][]string, 0, len(terms))
	for _, words := range terms {
		words = filterWords(words, pre)
		if len(words) == 0 {
			continue
		}
		if f.ignoreCase {
			for i, w := range words {
				words[i] = strings.ToLower(w)
			}
		}
		out = append(out, words)
	}
	return out
}

// filterWords runs words through filters, keeping a new slice of the
// resulting terms without stacked alternates.
func filterWords(words []string, filters []analysis.TokenFilter) []string {
	stream := make(analysis.TokenStream, len(words))
	start := 0
	for i, w := range words {
		stream[i] = &analysis.Token{Term: []byte(w), PositionIncr: 1, Start: start, End: start + len(w), Type: analysis.AlphaNumeric}
		start += len(w) + 1
	}
	for _, filter := range filters {
		stream = filter.Filter(stream)
	}
	out := make([]string, 0, len(stream))
	for _, t := range stream {
		if t.PositionIncr == 0 && len(out) > 0 {
			continue
		}
		out = append(out, string(t.Term))
	}
	return out
}

// ValidateSynonymRule reports whether rule is a valid Solr synonym rule.
func ValidateSynonymRule(rule string) error {
	_, _, err := parseSolrRule(rule, rule)
	return err
}

func (f *SynonymTokenFilter) Filter(input analysis.TokenStream) analysis.TokenStream {
	if len(f.rules) == 0 || len(input) == 0 {
		return input
	}

	positions := make([]int, len(input))
	pos := 0
	for i, t := range input {
		pos += t.PositionIncr
		positions[i] = pos
	}

	out := make([]positionedToken, 0, len(input))
	changed := false
	shift := 0 // positions taken by replacements longer than their match
	for i := 0; i < len(input); {
		n, reps := f.match(input, i)
		if n == 0 {
			out = append(out, positionedToken{pos: positions[i] + shift, tok: input[i]})
			i++
			continue
		}
		changed = true
		out = append(out, f.emit(input[i:i+n], positions[i]+shift, reps)...)
		longest := 0
		for _, rep := range reps {
			longest = max(longest, len(rep))
		}
		shift += max(0, longest-n)
		i += n
	}
	if !changed {
		return input
	}

	// multi-word replacements may overlap the following input tokens
	sort.SliceStable(out, func(a, b int) bool { return out[a].pos < out[b].pos })
	result := make(analysis.TokenStream, len(out))
	prev := 0
	for i, pt := range out {
		pt.tok.PositionIncr = pt.pos - prev
		prev = pt.pos
		result[i] = pt.tok
	}
	return result
}

// match returns the length and replacements of the longest rule matching
// the consecutive tokens starting at i, or 0 when none match.
func (f *SynonymTokenFilter) match(input analysis.TokenStream, i int) (int, [][]string) {
	limit := min(f.maxLen, len(input)-i)
	terms := make([]string, 0, limit)
	for k := 0; k < limit; k++ {
		if k > 0 && input[i+k].PositionIncr != 1 {
			break
		}
		terms = append(terms, f.term(input[i+k]))
	}
	for n := len(terms); n > 0; n-- {
		if reps, ok := f.rules[strings.Join(terms[:n], synonymKeySep)]; ok {
			return n, reps
		}
	}
	return 0, nil
}

// emit returns the tokens replacing matched at position pos: the original
// tokens first when they are one of the replacements, then the alternates.
func (f *SynonymTokenFilter) emit(matched analysis.TokenStream, pos int, reps [][]string) []positionedToken {
	terms := make([]string, len(matched))
	for k, t := range matched {
		terms[k] = f.term(t)
	}
	orig := strings.Join(terms, synonymKeySep)

	out := make([]positionedToken, 0, len(reps)*len(matched))
	kept := -1
	for j, rep := range reps {
		if strings.Join(rep, synonymKeySep) == orig {
			kept = j
			for k, t := range matched {
				out = append(out, positionedToken{pos: pos + k, tok: t})
			}
			break
		}
	}

	first, last := matched[0], matched[len(matched)-1]
	for j, rep := range reps {
		if j == kept {
			continue
		}
		for k, term := range rep {
			out = append(out, positionedToken{pos: pos + k, tok: &analysis.Token{
				Start: first.Start,
				End:   last.End,
				Term:  []byte(term),
				Type:  first.Type,
			}})
		}
	}
	return out
}

func (f *SynonymTokenFilter) term(t *analysis.Token) string {
	if f.ignoreCase {
		return strings.ToLower(string(t.Term))
	}
	return string(t.Term)
}

// addEquivalent maps each of terms to all of them, or to the first one
// without expand.
func (f *SynonymTokenFilter) addEquivalent(terms [][]string, expand bool) {
	to := terms
	if !expand {
		to = terms[:1]
	}
	for _, from := range terms {
		f.add(from, to)
	}
}

// add maps the term sequence from to each replacement in to, skipping
// replacements already present.
func (f *SynonymTokenFilter) add(from []string, to [][]string) {
	key := strings.Join(from, synonymKeySep)
	reps := f.rules[key]
	for _, rep := range to {
		repKey := strings.Join(rep, synonymKeySep)
		exists := false
		for _, have := range reps {
			if strings.Join(have, synonymKeySep) == repKey {
				exists = true
				break
			}
		}
		if !exists {
			reps = append(reps, rep)
		}
	}
	f.rules[key] = reps
	f.maxLen = max(f.maxLen, len(from))
}

// parseSolrRule parses `a, b => c, d` into the term sequences to match and
// their replacements, or `a, b, c` into its terms and nil replacements.
// A backslash escapes the next character, as in `foo\,bar => baz`. Errors
// refer to the rule by label.
func parseSolrRule(rule, label string) ([][]string, [][]string, error) {
	sides := splitUnescaped(rule, "=>")
	switch len(sides) {
	case 1:
		terms, err := parseSolrTerms(splitUnescaped(rule, ","), label)
		return terms, nil, err
	case 2:
		from, err := parseSolrTerms(splitUnescaped(sides[0], ","), label)
		if err != nil {
			return nil, nil, err
		}
		to, err := parseSolrTerms(splitUnescaped(sides[1], ","), label)
		if err != nil {
			return nil, nil, err
		}
		return from, to, nil
	default:
		return nil, nil, invalidSynonymRule(label, "more than one explicit mapping specified on the same line")
	}
}

// splitUnescaped splits s around each sep not escaped by a backslash.
func splitUnescaped(s, sep string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(s[i:], sep) {
			out = append(out, s[start:i])
			start = i + len(sep)
			i = start - 1
		}
	}
	return append(out, s[start:])
}

// unescape drops the backslash escaping a character.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseSolrTerms splits each term into its words.
func parseSolrTerms(terms []string, rule string) ([][]string, error) {
	out := make([][]string, 0, len(terms))
	for _, term := range terms {
		words := strings.Fields(unescape(term))
		if len(words) == 0 {
			return nil, invalidSynonymRule(rule, "term is empty")
		}
		out = append(out, words)
	}
	return out, nil
}

// parseWordnet groups the words of wordnet lines `s(100000001,1,'abstain',v,1,0).`
// by synset id, in order of first appearance.
func parseWordnet(lines []synonymRule, lenient bool) ([][]string, error) {
	var ids []string
	groups := make(map[string][]string)
	for _, l := range lines {
		id, word, ok := parseWordnetLine(l.text)
		if !ok {
			if lenient {
				continue
			}
			return nil, invalidSynonymRule(l.label, "invalid wordnet line")
		}
		if _, exists := groups[id]; !exists {
			ids = append(ids, id)
		}
		groups[id] = append(groups[id], word)
	}

	out := make([][]string, len(ids))
	for i, id := range ids {
		out[i] = groups[id]
	}
	return out, nil
}

func parseWordnetLine(l string) (string, string, bool) {
	if !strings.HasPrefix(l, "s(") || !strings.HasSuffix(l, ").") {
		return "", "", false
	}
	id, rest, ok := strings.Cut(l[2:], ",")
	if !ok || id == "" {
		return "", "", false
	}
	start := strings.IndexByte(rest, '\'')
	if start < 0 {
		return "", "", false
	}
	var word strings.Builder
	for i := start + 1; i < len(rest); i++ {
		if rest[i] != '\'' {
			word.WriteByte(rest[i])
			continue
		}
		if i+1 < len(rest) && rest[i+1] == '\'' {
			word.WriteByte('\'')
			i++
			continue
		}
		return id, word.String(), strings.TrimSpace(word.String()) != ""
	}
	return "", "", false
}

// synonymLines collects the non blank, non comment rules of every
// configured source.
func synonymLines(options interface{}) ([]synonymRule, error) {
	var raw []synonymRule
	inline := func(rules []string) {
		for _, r := range rules {
			r = strings.TrimSpace(r)
			raw = append(raw, synonymRule{text: r, label: r})
		}
	}
	found := false
	if _, err := zutils.GetAnyFromMap(options, "synonyms"); err == nil {
		rules, err := zutils.GetStringSliceFromMap(options, "synonyms")
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, "[synonym] option [synonyms] should be an array of string")
		}
		inline(rules)
		found = true
	}
	if _, err := zutils.GetAnyFromMap(options, "synonyms_set"); err == nil {
		id, err := zutils.GetStringFromMap(options, "synonyms_set")
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, "[synonym] option [synonyms_set] should be a string")
		}
		if SynonymSetLookup == nil {
			return nil, synonymSetNotFound(id)
		}
		rules, err := SynonymSetLookup(id)
		if err != nil {
			return nil, synonymSetNotFound(id)
		}
		inline(rules)
		found = true
	}
	if _, err := zutils.GetAnyFromMap(options, "synonyms_path"); err == nil {
		path, err := zutils.GetStringFromMap(options, "synonyms_path")
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, "[synonym] option [synonyms_path] should be a string")
		}
		data, err := readSynonymsPath(path)
		if err != nil {
			return nil, err
		}
		// errors cite the line number only, never the file content
		for i, l := range strings.Split(string(data), "\n") {
			raw = append(raw, synonymRule{text: strings.TrimSpace(l), label: fmt.Sprintf("synonyms_path line %d", i+1)})
		}
		found = true
	}
	if !found {
		return nil, errors.New(errors.ErrorTypeParsingException, "[synonym] requires one of [synonyms], [synonyms_set] or [synonyms_path] to be configured")
	}

	lines := make([]synonymRule, 0, len(raw))
	for _, l := range raw {
		if l.text == "" || strings.HasPrefix(l.text, "#") {
			continue
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// readSynonymsPath reads the file at path relative to the data path,
// refusing absolute paths and any path, symlinks included, leaving it.
func readSynonymsPath(path string) ([]byte, error) {
	if !filepath.IsLocal(path) {
		return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[synonym] synonyms_path [%s] must be a relative path inside the data path", path))
	}
	f, err := os.OpenInRoot(config.Global.DataPath, path)
	if err != nil {
		return nil, readSynonymsPathError(path)
	}
	data, err := io.ReadAll(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, readSynonymsPathError(path)
	}
	return data, nil
}

func readSynonymsPathError(path string) error {
	return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[synonym] failed to read synonyms_path [%s]", path))
}

func synonymBool(options interface{}, key string, def bool) bool {
	v, err := zutils.GetBoolFromMap(options, key)
	if err != nil {
		return def
	}
	return v
}

func synonymSetNotFound(id string) error {
	return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[synonym] synonyms set [%s] not found", id))
}

func invalidSynonymRule(rule, reason string) error {
	return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[synonym] invalid synonym rule [%s]: %s", rule, reason))
}
