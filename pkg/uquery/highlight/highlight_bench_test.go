/* Copyright 2026 AtomAI.
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

package highlight

import (
	"strings"
	"testing"

	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/analysis/analyzer"
	"github.com/vcaesar/riot/analysis/char"
	"github.com/vcaesar/riot/search"
)

func BenchmarkLocations(b *testing.B) {
	html := analyzer.NewStandardAnalyzer()
	html.CharFilters = []analysis.CharFilter{char.NewHTMLCharFilter()}
	value := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog ", 50))
	matched := search.TermLocationMap{"quick": {{Pos: 2}, {Pos: 11}}, "fox": {{Pos: 4}}, "dog": {{Pos: 9}}}
	for name, zer := range map[string]*analysis.Analyzer{"standard": analyzer.NewStandardAnalyzer(), "html_strip": html} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				Locations(zer, value, matched)
			}
		})
	}
}
