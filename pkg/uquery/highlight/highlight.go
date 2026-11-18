/* Copyright 2022 Zinc Labs Inc. and Contributors
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

package highlight

import (
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func Request(highlight *meta.Highlight) error {
	if len(highlight.Fields) == 0 {
		return nil
	}

	if highlight.NumberOfFragments == 0 {
		highlight.NumberOfFragments = 3
	}
	for _, field := range highlight.Fields {
		if field.FragmentSize == 0 && highlight.FragmentSize > 0 {
			field.FragmentSize = highlight.FragmentSize
		}
		if field.NumberOfFragments == 0 && highlight.NumberOfFragments > 0 {
			field.NumberOfFragments = highlight.NumberOfFragments
		}
	}

	return nil
}

// Locations returns the byte offsets of the matched terms in value, a stored
// value of a field analyzed by zer (nil for a keyword). The index keeps only
// term positions, so value is analyzed again and its tokens are matched by
// term and position; a value whose positions are shifted, such as a later
// element of an array, is matched by term alone.
func Locations(zer *analysis.Analyzer, value []byte, matched search.TermLocationMap) search.TermLocationMap {
	var tokens analysis.TokenStream
	if zer == nil {
		tokens = analysis.TokenStream{{Term: value, Start: 0, End: len(value), PositionIncr: 1}}
	} else {
		// analyzers may rewrite their input, such as lowercasing in place
		tokens = zer.Analyze(append([]byte(nil), value...))
	}

	positions := make(map[int]struct{})
	for _, locs := range matched {
		for _, loc := range locs {
			positions[loc.Pos] = struct{}{}
		}
	}

	byPos := make(search.TermLocationMap)
	byTerm := make(search.TermLocationMap)
	pos := 0
	for _, token := range tokens {
		pos += token.PositionIncr
		term := string(token.Term)
		if _, ok := matched[term]; !ok {
			continue
		}
		loc := &search.Location{Pos: pos, Start: token.Start, End: token.End}
		byTerm.AddLocation(term, loc)
		if _, ok := positions[pos]; ok {
			byPos.AddLocation(term, loc)
		}
	}
	if len(byPos) > 0 {
		return byPos
	}
	return byTerm
}
