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
	rules      map[string][][]string // matched terms joined by synonymKeySep -> replacements
	maxLen     int
	ignoreCase bool
}

type positionedToken struct {
	pos int
	tok *analysis.Token
}

// NewSynonymTokenFilter builds the synonym and synonym_graph token filters.
func NewSynonymTokenFilter(options interface{}) (analysis.TokenFilter, error) {
	lines, err := synonymLines(options)
	if err != nil {
		return nil, err
	}

	f := &SynonymTokenFilter{
		rules:      make(map[string][][]string),
		ignoreCase: synonymBool(options, "ignore_case", false),
	}
	expand := synonymBool(options, "expand", true)
	lenient := synonymBool(options, "lenient", false)
	format, _ := zutils.GetStringFromMap(options, "format")
	switch strings.ToLower(format) {
	case "", "solr":
		for _, line := range lines {
			from, to, err := parseSolrRule(line, f.ignoreCase)
			if err != nil {
				if lenient {
					continue
				}
				return nil, err
			}
			if to == nil {
				f.addEquivalent(from, expand)
				continue
			}
			for _, terms := range from {
				f.add(terms, to)
			}
		}
	case "wordnet":
		groups, err := parseWordnet(lines, lenient)
		if err != nil {
			return nil, err
		}
		for _, words := range groups {
			terms, err := parseSolrTerms(words, strings.Join(words, ", "), f.ignoreCase)
			if err != nil {
				if lenient {
					continue
				}
				return nil, err
			}
			f.addEquivalent(terms, expand)
		}
	default:
		return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[synonym] unsupported format [%s]", format))
	}

	return f, nil
}

// ValidateSynonymRule reports whether rule is a valid Solr synonym rule.
func ValidateSynonymRule(rule string) error {
	_, _, err := parseSolrRule(rule, false)
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
	for i := 0; i < len(input); {
		n, reps := f.match(input, i)
		if n == 0 {
			out = append(out, positionedToken{pos: positions[i], tok: input[i]})
			i++
			continue
		}
		changed = true
		out = append(out, f.emit(input[i:i+n], positions[i], reps)...)
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
func parseSolrRule(rule string, ignoreCase bool) ([][]string, [][]string, error) {
	sides := strings.Split(rule, "=>")
	switch len(sides) {
	case 1:
		terms, err := parseSolrTerms(strings.Split(rule, ","), rule, ignoreCase)
		return terms, nil, err
	case 2:
		from, err := parseSolrTerms(strings.Split(sides[0], ","), rule, ignoreCase)
		if err != nil {
			return nil, nil, err
		}
		to, err := parseSolrTerms(strings.Split(sides[1], ","), rule, ignoreCase)
		if err != nil {
			return nil, nil, err
		}
		return from, to, nil
	default:
		return nil, nil, invalidSynonymRule(rule, "more than one explicit mapping specified on the same line")
	}
}

// parseSolrTerms splits each term into its words.
func parseSolrTerms(terms []string, rule string, ignoreCase bool) ([][]string, error) {
	out := make([][]string, 0, len(terms))
	for _, term := range terms {
		if ignoreCase {
			term = strings.ToLower(term)
		}
		words := strings.Fields(term)
		if len(words) == 0 {
			return nil, invalidSynonymRule(rule, "term is empty")
		}
		out = append(out, words)
	}
	return out, nil
}

// parseWordnet groups the words of wordnet lines `s(100000001,1,'abstain',v,1,0).`
// by synset id, in order of first appearance.
func parseWordnet(lines []string, lenient bool) ([][]string, error) {
	var ids []string
	groups := make(map[string][]string)
	for _, l := range lines {
		id, word, ok := parseWordnetLine(l)
		if !ok {
			if lenient {
				continue
			}
			return nil, invalidSynonymRule(l, "invalid wordnet line")
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
		return id, word.String(), word.Len() > 0
	}
	return "", "", false
}

// synonymLines collects the non blank, non comment rules of every
// configured source.
func synonymLines(options interface{}) ([]string, error) {
	var raw []string
	found := false
	if _, err := zutils.GetAnyFromMap(options, "synonyms"); err == nil {
		rules, err := zutils.GetStringSliceFromMap(options, "synonyms")
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, "[synonym] option [synonyms] should be an array of string")
		}
		raw = append(raw, rules...)
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
		raw = append(raw, rules...)
		found = true
	}
	if _, err := zutils.GetAnyFromMap(options, "synonyms_path"); err == nil {
		path, err := zutils.GetStringFromMap(options, "synonyms_path")
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, "[synonym] option [synonyms_path] should be a string")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(config.Global.DataPath, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[synonym] failed to read synonyms_path [%s]", path)).Cause(err)
		}
		raw = append(raw, strings.Split(string(data), "\n")...)
		found = true
	}
	if !found {
		return nil, errors.New(errors.ErrorTypeParsingException, "[synonym] requires one of [synonyms], [synonyms_set] or [synonyms_path] to be configured")
	}

	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		lines = append(lines, l)
	}
	return lines, nil
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
